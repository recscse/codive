package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecentSyncFailuresCountsOnlyTheTail(t *testing.T) {
	log := filepath.Join(t.TempDir(), "codive.log")
	var sb strings.Builder
	// An old failure well outside the window, then a quiet stretch, then two
	// recent ones.
	sb.WriteString(`{"level":"WARN","msg":"Index auto-sync failed","error":"old failure"}` + "\n")
	for i := 0; i < logTailLines; i++ {
		sb.WriteString(`{"level":"INFO","msg":"Index auto-sync applied changes"}` + "\n")
	}
	sb.WriteString(`{"level":"WARN","msg":"Index auto-sync failed","error":"database is locked (5)"}` + "\n")
	sb.WriteString(`{"level":"WARN","msg":"Index auto-sync failed","error":"context deadline exceeded"}` + "\n")
	if err := os.WriteFile(log, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	n, latest := recentSyncFailures(log)
	if n != 2 || latest != "context deadline exceeded" {
		t.Errorf("got %d failures, latest %q; want 2, %q", n, latest, "context deadline exceeded")
	}
	if n, _ := recentSyncFailures(filepath.Join(t.TempDir(), "missing.log")); n != 0 {
		t.Errorf("a missing log reported %d failures", n)
	}
}
