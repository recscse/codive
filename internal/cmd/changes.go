// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/recscse/ctxd/internal/db"
	"github.com/recscse/ctxd/internal/git"
	"github.com/recscse/ctxd/internal/ui"
)

// RunChanges inspects uncommitted git changes and prints an AST-aware token-efficient summary.
func RunChanges(targetDir string, asJSON bool) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".ctxd", "index.db")
	dbConn, err := db.Open(dbPath)
	if err == nil {
		defer dbConn.Close()
	}

	ctx := context.Background()
	res, err := git.GetGitChanges(ctx, absDir, dbConn)
	if err != nil {
		return fmt.Errorf("failed to retrieve git changes: %w", err)
	}

	if asJSON {
		return ui.PrintJSON(res)
	}

	if res.TotalChanged == 0 {
		ui.Success("✓ Clean working tree: No uncommitted changes detected.")
		return nil
	}

	ui.CyanBold.Printf("🌿 Git Changes (%d files on branch '%s'):\n\n", res.TotalChanged, res.Branch)
	for _, f := range res.Files {
		statusColor := ui.Yellow.Sprint(f.Status)
		if f.Status == "added" || f.Status == "untracked" {
			statusColor = ui.GreenBold.Sprint(f.Status)
		} else if f.Status == "deleted" {
			statusColor = ui.Red.Sprint(f.Status)
		}

		fmt.Printf("• %s [%s]\n", ui.Bold.Sprint(f.Path), statusColor)
		if len(f.AffectedSymbols) > 0 {
			for _, sym := range f.AffectedSymbols {
				fmt.Printf("   %s %s\n", ui.Dim.Sprint("└─"), ui.CyanBold.Sprint(sym))
			}
		}
		fmt.Println()
	}

	return nil
}
