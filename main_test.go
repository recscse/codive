package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
