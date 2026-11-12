// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/recscse/codive/internal/ui"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
}

const (
	releasesPage     = "https://github.com/recscse/codive/releases/latest"
	downloadAttempts = 3
)

// upgrader holds everything RunUpgrade touches outside the process so the whole
// flow can be exercised against a fake release server in tests.
type upgrader struct {
	apiURL       string // latest-release JSON endpoint
	downloadBase string // releases/download base; "<base>/<tag>/<asset>"
	apiClient    *http.Client
	dlClient     *http.Client // long timeout: a release archive is several MB
	exePath      string
	goos, goarch string
	smoke        func(path string) error
	retryDelay   time.Duration
}

func defaultUpgrader() (*upgrader, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to locate current executable: %w", err)
	}
	exePath, _ = filepath.Abs(exePath)
	return &upgrader{
		apiURL:       "https://api.github.com/repos/recscse/codive/releases/latest",
		downloadBase: "https://github.com/recscse/codive/releases/download",
		apiClient:    &http.Client{Timeout: 15 * time.Second},
		dlClient:     &http.Client{Timeout: 5 * time.Minute},
		exePath:      exePath,
		goos:         runtime.GOOS,
		goarch:       runtime.GOARCH,
		smoke:        smokeTestBinary,
		retryDelay:   2 * time.Second,
	}, nil
}

// RunUpgrade checks GitHub for the latest release and self-updates the binary in-place.
func RunUpgrade(currentVersion string) error {
	u, err := defaultUpgrader()
	if err != nil {
		return err
	}
	return u.run(currentVersion)
}

// manualInstallHelp tells the user how to upgrade without a Go toolchain.
func manualInstallHelp() {
	fmt.Println("  Upgrade manually (no Go needed) by re-running the installer:")
	fmt.Println("    Windows PowerShell: irm https://recscse.github.io/codive/install.ps1 | iex")
	fmt.Println("    macOS / Linux:      curl -fsSL https://recscse.github.io/codive/install.sh | sh")
	fmt.Println("  or download the archive for your platform from " + releasesPage)
}

func (u *upgrader) run(currentVersion string) error {
	ui.Header("codive — Self-Update & Version Check")
	ui.Divider()
	ui.KeyValue("Current Version", currentVersion)
	ui.KeyValue("Platform", fmt.Sprintf("%s/%s", u.goos, u.goarch))

	req, err := http.NewRequest("GET", u.apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create update request: %w", err)
	}
	req.Header.Set("User-Agent", "codive-cli")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := u.apiClient.Do(req)
	if err != nil {
		ui.Warning(fmt.Sprintf("Could not reach GitHub to check for updates: %v", err))
		manualInstallHelp()
		return fmt.Errorf("update check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		ui.Warning(fmt.Sprintf("GitHub returned HTTP %d while checking for updates (rate limit or outage).", resp.StatusCode))
		manualInstallHelp()
		return fmt.Errorf("update check failed: HTTP %d", resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return fmt.Errorf("failed to decode release payload: %w", err)
	}

	latestTag := strings.TrimSpace(rel.TagName)
	ui.KeyValueHighlight("Latest Release", latestTag)
	ui.Divider()
	fmt.Println()

	if latestTag == currentVersion || latestTag == "v"+currentVersion || currentVersion == "v"+latestTag {
		ui.Success(fmt.Sprintf("codive is already up to date (%s)!", currentVersion))
		return nil
	}

	ui.Info(fmt.Sprintf("New version available: %s -> %s", currentVersion, latestTag))

	var assetName string
	if u.goos == "windows" {
		assetName = fmt.Sprintf("codive_%s_windows_%s.zip", latestTag, u.goarch)
	} else {
		assetName = fmt.Sprintf("codive_%s_%s_%s.tar.gz", latestTag, u.goos, u.goarch)
	}
	downloadURL := fmt.Sprintf("%s/%s/%s", u.downloadBase, latestTag, assetName)

	fmt.Printf("  Downloading %s ...\n", assetName)
	bodyBytes, err := u.download(downloadURL)
	if err != nil {
		ui.Warning(fmt.Sprintf("Could not download %s: %v", assetName, err))
		manualInstallHelp()
		return fmt.Errorf("download failed: %w", err)
	}

	// Integrity check against the release's published checksum. This catches
	// corrupted or truncated downloads; it can't prove authenticity, since
	// the checksum comes from the same release.
	switch expected, err := fetchChecksum(u.dlClient, downloadURL+".sha256"); {
	case err != nil:
		ui.Warning(fmt.Sprintf("No checksum published for %s (%v); the download could not be verified.", assetName, err))
	case !checksumMatches(bodyBytes, expected):
		return fmt.Errorf("checksum mismatch for %s: download is corrupted or was tampered with; nothing was changed", assetName)
	default:
		ui.Info("Checksum verified (SHA-256).")
	}

	newBinaryBytes, err := extractBinary(bodyBytes, u.goos)
	if err != nil {
		return err
	}

	// Stage the new binary and make sure it actually runs on this machine
	// before replacing the working one, so a bad download can't leave the
	// user without a usable codive.
	exePath := u.exePath
	newExePath := exePath + ".new"
	if err := os.WriteFile(newExePath, newBinaryBytes, 0755); err != nil {
		return fmt.Errorf("failed to stage updated binary: %w", err)
	}
	if err := u.smoke(newExePath); err != nil {
		_ = os.Remove(newExePath)
		return fmt.Errorf("downloaded binary failed to run (%v); nothing was changed", err)
	}

	// Windows can't overwrite a running executable, but it can rename it.
	oldExePath := exePath + ".old"
	_ = os.Remove(oldExePath)
	if err := os.Rename(exePath, oldExePath); err != nil {
		_ = os.Remove(newExePath)
		return fmt.Errorf("failed to move the current binary aside: %w", err)
	}
	if err := os.Rename(newExePath, exePath); err != nil {
		_ = os.Rename(oldExePath, exePath)
		_ = os.Remove(newExePath)
		return fmt.Errorf("failed to install updated binary: %w", err)
	}
	// Fails harmlessly on Windows while the old binary is still running; it
	// is cleaned up by the next upgrade.
	_ = os.Remove(oldExePath)

	ui.Success(fmt.Sprintf("Successfully upgraded codive to %s at %s!", latestTag, exePath))
	return nil
}

// download fetches url, retrying transient failures (network errors, 5xx).
// A 4xx is final. The returned error says what actually went wrong.
func (u *upgrader) download(url string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(u.retryDelay)
		}
		resp, err := u.dlClient.Get(url)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
			if resp.StatusCode < 500 {
				return nil, lastErr
			}
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("download interrupted: %w", err)
			continue
		}
		return data, nil
	}
	return nil, lastErr
}

// extractBinary pulls the codive executable out of a release archive.
func extractBinary(archive []byte, goos string) ([]byte, error) {
	var out []byte
	if goos == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("failed to parse zip archive: %w", err)
		}
		for _, f := range zr.File {
			if f.Name == "codive.exe" || strings.HasSuffix(f.Name, "/codive.exe") {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				out, err = io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return nil, fmt.Errorf("failed to read codive.exe from archive: %w", err)
				}
				break
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, fmt.Errorf("failed to parse tar.gz archive: %w", err)
		}
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if hdr.Name == "codive" || strings.HasSuffix(hdr.Name, "/codive") {
				out, err = io.ReadAll(tr)
				if err != nil {
					return nil, fmt.Errorf("failed to read codive from archive: %w", err)
				}
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("could not extract codive executable from download archive")
	}
	return out, nil
}

// fetchChecksum downloads a "<hex digest>  <file name>" checksum file (the
// format sha256sum writes) and returns the digest.
func fetchChecksum(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	return parseChecksum(string(data))
}

func parseChecksum(text string) (string, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum file")
	}
	digest := strings.ToLower(fields[0])
	if len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("malformed SHA-256 digest")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("malformed SHA-256 digest")
	}
	return digest, nil
}

func checksumMatches(data []byte, expected string) bool {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == expected
}

// smokeTestBinary runs `<path> version` and fails if it doesn't exit cleanly,
// catching corrupted, truncated, or wrong-architecture binaries.
func smokeTestBinary(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version", "--no-color")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}
