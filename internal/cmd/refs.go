// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/ui"
)

// RunRefs locates and displays references/call-sites of a symbol across the repository.
func RunRefs(targetDir string, symbol string, limit int, asJSON bool) error {
	if strings.TrimSpace(symbol) == "" {
		return fmt.Errorf("symbol cannot be empty")
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".codive", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("repository is not initialized (no index found at %s). Run 'codive init' first", dbPath)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	ctx := context.Background()
	refs, err := db.FindReferences(ctx, database, symbol, limit)
	if err != nil {
		return fmt.Errorf("failed to find references: %w", err)
	}

	if asJSON {
		return ui.PrintJSON(refs)
	}

	if len(refs) == 0 {
		ui.Warning(fmt.Sprintf("No references found for '%s'", symbol))
		return nil
	}

	ui.CyanBold.Printf("Found %d reference(s) for '%s':\n\n", len(refs), symbol)
	for i, ref := range refs {
		fmt.Printf("%2d. 📍 %s:%s\n", i+1, ui.GreenBold.Sprint(ref.FilePath), ui.Yellow.Sprintf("%d", ref.LineNumber))
		fmt.Printf("    %s %s\n\n", ui.Dim.Sprint("│"), ref.Snippet)
	}

	return nil
}
