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

// RunUpgrade checks GitHub for the latest release and self-updates the binary in-place.
func RunUpgrade(currentVersion string) error {
	ui.Header("codive — Self-Update & Version Check")
	ui.Divider()
	ui.KeyValue("Current Version", currentVersion)
	ui.KeyValue("Platform", fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/recscse/codive/releases/latest", nil)
	if err != nil {
		return fmt.Errorf("failed to create update request: %w", err)
	}
	req.Header.Set("User-Agent", "codive-cli")

	resp, err := client.Do(req)
	if err != nil {
		ui.Warning(fmt.Sprintf("Could not connect to GitHub API: %v", err))
		fmt.Println("  You can upgrade manually by running: go install github.com/recscse/codive@latest")
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		ui.Info(fmt.Sprintf("GitHub API returned HTTP %d. Already on official release (%s).", resp.StatusCode, currentVersion))
		return nil
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
	fmt.Println("  Downloading update from GitHub Releases...")

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate current executable: %w", err)
	}
	exePath, _ = filepath.Abs(exePath)

	// Construct asset download URL
	var assetName string
	if runtime.GOOS == "windows" {
		assetName = fmt.Sprintf("codive_%s_windows_%s.zip", latestTag, runtime.GOARCH)
	} else {
		assetName = fmt.Sprintf("codive_%s_%s_%s.tar.gz", latestTag, runtime.GOOS, runtime.GOARCH)
	}

	downloadURL := fmt.Sprintf("https://github.com/recscse/codive/releases/download/%s/%s", latestTag, assetName)

	dlResp, err := client.Get(downloadURL)
	if err != nil || dlResp.StatusCode != http.StatusOK {
		// Fallback to Go install if direct asset download is unavailable
		ui.Warning(fmt.Sprintf("Binary asset '%s' not found on release. Falling back to Go install...", assetName))
		fmt.Println("  Run: go install github.com/recscse/codive@latest")
		return nil
	}
	defer dlResp.Body.Close()

	bodyBytes, err := io.ReadAll(dlResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read downloaded binary: %w", err)
	}

	// Integrity check against the release's published checksum. This catches
	// corrupted or truncated downloads; it can't prove authenticity, since
	// the checksum comes from the same release.
	switch expected, err := fetchChecksum(client, downloadURL+".sha256"); {
	case err != nil:
		ui.Warning(fmt.Sprintf("No checksum published for %s (%v); the download could not be verified.", assetName, err))
	case !checksumMatches(bodyBytes, expected):
		return fmt.Errorf("checksum mismatch for %s: download is corrupted or was tampered with; nothing was changed", assetName)
	default:
		ui.Info("Checksum verified (SHA-256).")
	}

	var newBinaryBytes []byte
	if runtime.GOOS == "windows" {
		zipReader, err := zip.NewReader(bytes.NewReader(bodyBytes), int64(len(bodyBytes)))
		if err != nil {
			return fmt.Errorf("failed to parse zip archive: %w", err)
		}
		for _, f := range zipReader.File {
			if strings.HasSuffix(f.Name, "codive.exe") || f.Name == "codive.exe" {
				rc, err := f.Open()
				if err != nil {
					return err
				}
				newBinaryBytes, err = io.ReadAll(rc)
				rc.Close()
				break
			}
		}
	} else {
		gzReader, err := gzip.NewReader(bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("failed to parse tar.gz archive: %w", err)
		}
		tarReader := tar.NewReader(gzReader)
		for {
			hdr, err := tarReader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if strings.HasSuffix(hdr.Name, "codive") {
				newBinaryBytes, err = io.ReadAll(tarReader)
				break
			}
		}
	}

	if len(newBinaryBytes) == 0 {
		return fmt.Errorf("could not extract codive executable from download archive")
	}

	// Stage the new binary and make sure it actually runs on this machine
	// before replacing the working one, so a bad download can't leave the
	// user without a usable codive.
	newExePath := exePath + ".new"
	if err := os.WriteFile(newExePath, newBinaryBytes, 0755); err != nil {
		return fmt.Errorf("failed to stage updated binary: %w", err)
	}
	if err := smokeTestBinary(newExePath); err != nil {
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
