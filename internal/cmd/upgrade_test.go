package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
