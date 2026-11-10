package cmd

import (
	"os"
	"testing"
)

// TestMain points the home directory at a temporary folder, so tests that run
// init don't record their temporary repos in the developer's real project list.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "codive-test-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
