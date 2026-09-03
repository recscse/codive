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

// RunStats prints the real-time token & money savings report.
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

	minutesSaved := float64(savings.TotalLatencySavedMs) / 60000.0

	ui.SectionHeader("Efficiency & Cost Savings")
	ui.KeyValue("Searches Served", ui.Count(int(savings.TotalQueriesServed), "query", "queries"))
	ui.KeyValueAccent("Latency Reduced", fmt.Sprintf("%.1f minutes saved", minutesSaved))
	ui.KeyValueAccent("Tokens Saved", fmt.Sprintf("%s tokens vs raw grep", formatTokens(savings.TotalTokensSaved)))
	ui.KeyValue("Cloud Savings", fmt.Sprintf("$%.2f USD (Claude/GPT-4o)", savings.EstimatedCostSavedUSD))
	ui.KeyValue("Speed Multiplier", fmt.Sprintf("%.1fx faster agent turns", savings.SpeedMultiplier))
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
