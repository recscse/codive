// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/recscse/ctxd/internal/db"
	"github.com/recscse/ctxd/internal/ui"
)

// RunStats prints the real-time token & money savings report.
func RunStats(targetDir string, asJSON bool) error {
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
	savings, err := db.GetSavingsReport(ctx, database)
	if err != nil {
		return err
	}

	if asJSON {
		return ui.PrintJSON(savings)
	}

	minutesSaved := float64(savings.TotalLatencySavedMs) / 60000.0

	fmt.Println()
	ui.CyanBold.Println("┌─────────────────────────────────────────────────────────────┐")
	ui.CyanBold.Println("│  ⚡ ctxd AI Efficiency & Cost Savings Report                │")
	ui.CyanBold.Println("├─────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  🔍 Agent Searches Served:     %-28s │\n", ui.Bold.Sprintf("%d queries", savings.TotalQueriesServed))
	fmt.Printf("│  ⏱️  Total Latency Reduced:     %-28s │\n", ui.GreenBold.Sprintf("%.1f minutes saved", minutesSaved))
	fmt.Printf("│  🪙  Tokens Saved vs Raw Grep:  %-28s │\n", ui.GreenBold.Sprintf("%s tokens", formatTokens(savings.TotalTokensSaved)))
	fmt.Printf("│  💰 Estimated Cloud Savings:   %-28s │\n", ui.Yellow.Sprintf("$%.2f USD (Claude/GPT-4o)", savings.EstimatedCostSavedUSD))
	fmt.Printf("│  🚀 Speed Multiplier:          %-28s │\n", ui.CyanBold.Sprintf("%.1fx FASTER agent turns", savings.SpeedMultiplier))
	ui.CyanBold.Println("└─────────────────────────────────────────────────────────────┘")
	fmt.Println()
	fmt.Printf("  %s %s\n", ui.Dim.Sprint("Embed in README.md:"), ui.Bold.Sprint("[![ctxd-token-reduction](https://img.shields.io/badge/Tokens_Saved-"+formatTokens(savings.TotalTokensSaved)+"-brightgreen)](#)"))
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
