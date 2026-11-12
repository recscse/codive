package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseChecksum(t *testing.T) {
	sum := sha256.Sum256([]byte("payload"))
	digest := hex.EncodeToString(sum[:])

	for _, text := range []string{
		digest + "  codive_v1.2.0_linux_amd64.tar.gz\n",
		strings.ToUpper(digest) + "\n",
		digest,
	} {
		got, err := parseChecksum(text)
		if err != nil || got != digest {
			t.Errorf("parseChecksum(%q) = %q, %v", text, got, err)
		}
	}
	for _, bad := range []string{"", "not-a-digest file", strings.Repeat("z", 64)} {
		if _, err := parseChecksum(bad); err == nil {
			t.Errorf("parseChecksum(%q) accepted a malformed digest", bad)
		}
	}

	if !checksumMatches([]byte("payload"), digest) {
		t.Error("matching payload rejected")
	}
	if checksumMatches([]byte("tampered"), digest) {
		t.Error("tampered payload accepted")
	}
}

// A corrupted download must be caught before it replaces the working binary.
func TestSmokeTestRejectsCorruptBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codive-new.exe")
	if err := os.WriteFile(path, []byte("this is not an executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := smokeTestBinary(path); err == nil {
		t.Error("corrupt binary passed the smoke test")
	}
}

// ---- Integration tests: the whole upgrade flow against a fake release server ----

type fakeRelease struct {
	tag      string
	archive  []byte
	checksum string // "" = derive from archive; "none" = serve 404
	// failFirst makes the first N archive requests return failStatus.
	failFirst  int
	failStatus int
	archiveHit int
	delay      time.Duration
	apiStatus  int
}

func (f *fakeRelease) server(t *testing.T, asset string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest", func(w http.ResponseWriter, r *http.Request) {
		if f.apiStatus != 0 {
			w.WriteHeader(f.apiStatus)
			return
		}
		fmt.Fprintf(w, `{"tag_name":%q}`, f.tag)
	})
	mux.HandleFunc("/dl/"+f.tag+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		f.archiveHit++
		if f.archiveHit <= f.failFirst {
			w.WriteHeader(f.failStatus)
			return
		}
		time.Sleep(f.delay)
		w.Write(f.archive)
	})
	mux.HandleFunc("/dl/"+f.tag+"/"+asset+".sha256", func(w http.ResponseWriter, r *http.Request) {
		switch f.checksum {
		case "none":
			w.WriteHeader(http.StatusNotFound)
		case "":
			sum := sha256.Sum256(f.archive)
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		default:
			fmt.Fprintf(w, "%s  %s\n", f.checksum, asset)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func makeArchive(t *testing.T, goos string, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("codive.exe")
		w.Write(payload)
		zw.Close()
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "codive", Mode: 0755, Size: int64(len(payload))})
	tw.Write(payload)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func assetFor(goos, goarch, tag string) string {
	if goos == "windows" {
		return fmt.Sprintf("codive_%s_windows_%s.zip", tag, goarch)
	}
	return fmt.Sprintf("codive_%s_%s_%s.tar.gz", tag, goos, goarch)
}

// newTestUpgrader wires an upgrader to srv with a throwaway "installed" binary.
func newTestUpgrader(t *testing.T, srv *httptest.Server, goos, goarch string) (*upgrader, string) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "codive.exe")
	if err := os.WriteFile(exe, []byte("OLD"), 0755); err != nil {
		t.Fatal(err)
	}
	return &upgrader{
		apiURL:       srv.URL + "/api/latest",
		downloadBase: srv.URL + "/dl",
		apiClient:    srv.Client(),
		dlClient:     srv.Client(),
		exePath:      exe,
		goos:         goos,
		goarch:       goarch,
		smoke:        func(string) error { return nil },
		retryDelay:   time.Millisecond,
	}, exe
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpgradeEveryPlatform(t *testing.T) {
	for _, p := range [][2]string{
		{"windows", "amd64"}, {"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"},
	} {
		t.Run(p[0]+"_"+p[1], func(t *testing.T) {
			asset := assetFor(p[0], p[1], "v9.9.9")
			rel := &fakeRelease{tag: "v9.9.9", archive: makeArchive(t, p[0], []byte("NEW"))}
			u, exe := newTestUpgrader(t, rel.server(t, asset), p[0], p[1])
			if err := u.run("v1.2.0"); err != nil {
				t.Fatalf("upgrade failed: %v", err)
			}
			if got := readFile(t, exe); got != "NEW" {
				t.Errorf("binary = %q, want NEW", got)
			}
		})
	}
}

func TestUpgradeAlreadyCurrent(t *testing.T) {
	asset := assetFor("linux", "amd64", "v1.3.2")
	rel := &fakeRelease{tag: "v1.3.2", archive: makeArchive(t, "linux", []byte("NEW"))}
	u, exe := newTestUpgrader(t, rel.server(t, asset), "linux", "amd64")
	if err := u.run("v1.3.2"); err != nil {
		t.Fatal(err)
	}
	if rel.archiveHit != 0 || readFile(t, exe) != "OLD" {
		t.Error("up-to-date install must not download or modify anything")
	}
}

// The original bug: a failed download was reported as a soft "fall back to Go install".
func TestUpgradeMissingAssetFailsLoudlyAndKeepsBinary(t *testing.T) {
	rel := &fakeRelease{tag: "v9.9.9", archive: makeArchive(t, "linux", []byte("NEW"))}
	// Server only knows a different asset name, so ours gets a 404.
	u, exe := newTestUpgrader(t, rel.server(t, "other.tar.gz"), "linux", "amd64")
	err := u.run("v1.2.0")
	if err == nil {
		t.Fatal("missing asset must return an error (non-zero exit), not nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should state the real cause (HTTP 404), got: %v", err)
	}
	if readFile(t, exe) != "OLD" {
		t.Error("working binary was modified")
	}
}

func TestUpgradeRetriesTransientServerErrors(t *testing.T) {
	asset := assetFor("linux", "amd64", "v9.9.9")
	rel := &fakeRelease{tag: "v9.9.9", archive: makeArchive(t, "linux", []byte("NEW")), failFirst: 2, failStatus: 503}
	u, exe := newTestUpgrader(t, rel.server(t, asset), "linux", "amd64")
	if err := u.run("v1.2.0"); err != nil {
		t.Fatalf("should recover after transient 503s: %v", err)
	}
	if readFile(t, exe) != "NEW" {
		t.Error("binary not upgraded after retry")
	}
}

func TestUpgradeGivesUpAfterRepeatedFailures(t *testing.T) {
	asset := assetFor("linux", "amd64", "v9.9.9")
	rel := &fakeRelease{tag: "v9.9.9", archive: makeArchive(t, "linux", []byte("NEW")), failFirst: 99, failStatus: 502}
	u, exe := newTestUpgrader(t, rel.server(t, asset), "linux", "amd64")
	if err := u.run("v1.2.0"); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if rel.archiveHit != downloadAttempts {
		t.Errorf("attempts = %d, want %d", rel.archiveHit, downloadAttempts)
	}
	if readFile(t, exe) != "OLD" {
		t.Error("working binary was modified")
	}
}

func TestUpgradeRejectsChecksumMismatch(t *testing.T) {
	asset := assetFor("linux", "amd64", "v9.9.9")
	rel := &fakeRelease{tag: "v9.9.9", archive: makeArchive(t, "linux", []byte("NEW")), checksum: strings.Repeat("0", 64)}
	u, exe := newTestUpgrader(t, rel.server(t, asset), "linux", "amd64")
	if err := u.run("v1.2.0"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch error, got %v", err)
	}
	if readFile(t, exe) != "OLD" {
		t.Error("working binary was modified")
	}
}

func TestUpgradeToleratesMissingChecksumFile(t *testing.T) {
	asset := assetFor("linux", "amd64", "v9.9.9")
	rel := &fakeRelease{tag: "v9.9.9", archive: makeArchive(t, "linux", []byte("NEW")), checksum: "none"}
	u, exe := newTestUpgrader(t, rel.server(t, asset), "linux", "amd64")
	if err := u.run("v1.2.0"); err != nil {
		t.Fatal(err)
	}
	if readFile(t, exe) != "NEW" {
		t.Error("binary not upgraded")
	}
}

func TestUpgradeBrokenBinaryIsNeverInstalled(t *testing.T) {
	asset := assetFor("linux", "amd64", "v9.9.9")
	rel := &fakeRelease{tag: "v9.9.9", archive: makeArchive(t, "linux", []byte("NEW"))}
	u, exe := newTestUpgrader(t, rel.server(t, asset), "linux", "amd64")
	u.smoke = func(string) error { return fmt.Errorf("exec format error") }
	if err := u.run("v1.2.0"); err == nil {
		t.Fatal("expected smoke test failure")
	}
	if readFile(t, exe) != "OLD" {
		t.Error("working binary was replaced by a broken one")
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Error("staged binary left behind")
	}
}

func TestUpgradeGitHubAPIErrorIsReported(t *testing.T) {
	rel := &fakeRelease{tag: "v9.9.9", apiStatus: http.StatusForbidden}
	u, _ := newTestUpgrader(t, rel.server(t, "x"), "linux", "amd64")
	if err := u.run("v1.2.0"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want HTTP 403 error, got %v", err)
	}
}

// The short API timeout must not cut off a slow archive download.
func TestUpgradeSlowDownloadOutlivesAPITimeout(t *testing.T) {
	asset := assetFor("linux", "amd64", "v9.9.9")
	rel := &fakeRelease{tag: "v9.9.9", archive: makeArchive(t, "linux", []byte("NEW")), delay: 1500 * time.Millisecond}
	u, exe := newTestUpgrader(t, rel.server(t, asset), "linux", "amd64")
	u.apiClient = &http.Client{Timeout: 1 * time.Second}
	u.dlClient = &http.Client{Timeout: 30 * time.Second}
	// The API call is instant; only the download is slow.
	if err := u.run("v1.2.0"); err != nil {
		t.Fatalf("slow download failed: %v", err)
	}
	if readFile(t, exe) != "NEW" {
		t.Error("binary not upgraded")
	}
}

func TestExtractBinaryRejectsArchiveWithoutExecutable(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("README.txt")
	w.Write([]byte("hi"))
	zw.Close()
	if _, err := extractBinary(buf.Bytes(), "windows"); err == nil {
		t.Error("archive without codive.exe accepted")
	}
	if _, err := extractBinary([]byte("garbage"), "linux"); err == nil {
		t.Error("garbage archive accepted")
	}
}

// End to end with a REAL codive binary: build it, ship it through the fake
// release server, upgrade a stand-in install, and run the result.
func TestUpgradeEndToEndWithRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the real binary")
	}
	built := filepath.Join(t.TempDir(), "codive-real"+exeSuffix())
	build := exec.Command("go", "build", "-ldflags", "-X main.Version=v9.9.9", "-o", built, "../..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	payload, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	asset := assetFor(runtime.GOOS, runtime.GOARCH, "v9.9.9")
	rel := &fakeRelease{tag: "v9.9.9", archive: makeArchive(t, runtime.GOOS, payload)}
	u, exe := newTestUpgrader(t, rel.server(t, asset), runtime.GOOS, runtime.GOARCH)
	u.smoke = smokeTestBinary // the real smoke test
	if err := u.run("v1.2.0"); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	out, err := exec.Command(exe, "version", "--no-color").CombinedOutput()
	if err != nil {
		t.Fatalf("upgraded binary does not run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "9.9.9") {
		t.Errorf("upgraded binary reports wrong version: %s", out)
	}
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// Release-pipeline contract: the asset names the workflow publishes must be the
// ones the upgrader and every installer request. A rename on either side fails
// here instead of in users' terminals after a release.
func TestReleaseAssetNamesMatchWorkflowAndInstallers(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	wf := read(".github/workflows/release.yml")
	for _, want := range []string{
		`codive_${TAG_NAME}_${{ matrix.goos }}_${{ matrix.goarch }}.zip`,
		`codive_${TAG_NAME}_${{ matrix.goos }}_${{ matrix.goarch }}.tar.gz`,
		`.sha256`,
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("release.yml no longer produces %q", want)
		}
	}
	for file, want := range map[string]string{
		"install.ps1": `codive_${Tag}_windows_amd64.zip`,
		"install.sh":  `codive_${TAG}_${OS}_${ARCH}.tar.gz`,
		"install.cmd": `codive_%TAG%_windows_amd64.zip`,
	} {
		for _, dir := range []string{"", "site/"} {
			if !strings.Contains(read(dir+file), want) {
				t.Errorf("%s%s does not request %q", dir, file, want)
			}
		}
	}
	if assetFor("windows", "amd64", "v1") != "codive_v1_windows_amd64.zip" ||
		assetFor("linux", "arm64", "v1") != "codive_v1_linux_arm64.tar.gz" {
		t.Error("upgrader asset naming drifted from the release workflow")
	}
}
