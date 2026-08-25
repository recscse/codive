// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/recscse/ctxd/internal/ui"
)

// RunReindex wipes existing index database and rebuilds the index completely from scratch.
func RunReindex(targetDir string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".ctxd", "index.db")
	if _, err := os.Stat(dbPath); err == nil {
		ui.Warning(fmt.Sprintf("Removing existing index at %s", dbPath))
		_ = os.Remove(dbPath)
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
	}

	return RunInit(absDir)
}
