package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/recscse/devctx/internal/db"
	"github.com/recscse/devctx/internal/ui"
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

	dbPath := filepath.Join(absDir, ".devctx", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("repository not initialized — no index at %s\n  Run 'devctx init' first", dbPath)
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

	ui.SectionHeader(fmt.Sprintf("Symbol Results  (%d matches for '%s')", len(results), query))

	for i, s := range results {
		fmt.Println()
		fmt.Printf("  %s  %s  %s\n",
			ui.Dim.Sprintf("%2d.", i+1),
			ui.GreenBold.Sprint(s.Name),
			ui.Dim.Sprintf("[%s]", s.Kind),
		)
		fmt.Printf("     %s  %s:%d\n",
			ui.Dim.Sprint("file"),
			ui.Bold.Sprint(s.FilePath),
			s.LineNumber,
		)
		if s.Signature != "" && s.Signature != s.Name {
			fmt.Printf("     %s  %s\n",
				ui.Dim.Sprint("sig "),
				ui.Dim.Sprint(truncate(s.Signature, 90)),
			)
		}
	}
	fmt.Println()
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
