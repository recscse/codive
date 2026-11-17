// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/ui"
)

// RunStats prints recorded tool usage and the token counts that were measured.
func RunStats(targetDir string, asJSON bool) error {
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
	savings, err := db.GetSavingsReport(ctx, database)
	if err != nil {
		return err
	}

	if asJSON {
		return ui.PrintJSON(savings)
	}

	ui.SectionHeader("Usage")
	ui.KeyValue("Queries Served", ui.Count(int(savings.TotalQueriesServed), "query", "queries"))
	ui.KeyValue("Partial Reads", ui.Count(int(savings.PartialReads), "call", "calls")+" returned an outline or one definition instead of a whole file")
	if savings.PartialReads > 0 {
		ui.KeyValueAccent("Tokens Returned", fmt.Sprintf("%s, from files totalling %s (%s fewer)",
			formatTokens(savings.TokensReturned), formatTokens(savings.WholeFileTokens), formatTokens(savings.TotalTokensSaved)))
	}
	fmt.Println()
	fmt.Println("  Token counts are estimates at 4 characters per token and cover partial reads only.")
	fmt.Println("  Searches and reference lookups are counted as queries; they have no measured")
	fmt.Println("  baseline, so no saving is claimed for them.")
	fmt.Println()

	return nil
}

func formatTokens(t int64) string {
	if t >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(t)/1000000.0)
	}
	if t >= 1000 {
		return fmt.Sprintf("%.1fK", float64(t)/1000.0)
	}
	return fmt.Sprintf("%d", t)
}
