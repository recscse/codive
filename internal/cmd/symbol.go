package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/recscse/ctxd/internal/db"
	"github.com/recscse/ctxd/internal/ui"
)

// RunFindSymbol searches for symbols matching a query name or signature.
func RunFindSymbol(targetDir string, query string, asJSON bool) error {
	if query == "" {
		return fmt.Errorf("symbol query cannot be empty")
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".ctxd", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("repository is not initialized (no index found at %s). Run 'ctxd init' first", dbPath)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	ctx := context.Background()
	results, err := db.FindSymbols(ctx, database, query)
	if err != nil {
		return fmt.Errorf("symbol query failed: %w", err)
	}

	if asJSON {
		return ui.PrintJSON(results)
	}

	if len(results) == 0 {
		ui.Warning(fmt.Sprintf("No symbols found matching '%s'", query))
		return nil
	}

	ui.CyanBold.Printf("Found %d symbol(s) matching '%s':\n\n", len(results), query)
	for i, s := range results {
		fmt.Printf("%2d. [%s] %s\n", i+1, ui.Magenta.Sprint(s.Kind), ui.Bold.Sprint(s.Name))
		fmt.Printf("    %s  %s:%d\n", ui.Dim.Sprint("Location:"), ui.Green.Sprint(s.FilePath), s.LineNumber)
		fmt.Printf("    %s %s\n\n", ui.Dim.Sprint("Signature:"), s.Signature)
	}

	return nil
}
