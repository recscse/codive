package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Releases are published automatically when main.go's Version has no release
// yet, and the notes come from CHANGELOG.md. Fail here, on the PR, if the
// version has no changelog section instead of failing after the merge.
func TestVersionHasChangelogEntry(t *testing.T) {
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(Version, "v") {
		t.Fatalf("Version %q must start with v", Version)
	}
	if !strings.Contains(string(data), "## ["+Version+"]") {
		t.Errorf("CHANGELOG.md has no '## [%s]' section; add release notes for this version", Version)
	}
}

func TestLogDirFor(t *testing.T) {
	tempDir := t.TempDir()
	missing := filepath.Join(tempDir, "GenerateToken")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no args", []string{"status"}, "."},
		{"directory arg", []string{"init", tempDir}, tempDir},
		{"symbol arg is not a directory", []string{"blast", missing}, "."},
		{"symbol then directory", []string{"symbol", "Foo", tempDir}, tempDir},
		{"flag is skipped", []string{"serve", "--verbose"}, "."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := logDirFor(tt.args); got != tt.want {
				t.Errorf("logDirFor(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}

	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("logDirFor must not create directories, but %s exists", missing)
	}
}
