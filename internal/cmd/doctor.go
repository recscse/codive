// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/recscse/ctxd/internal/db"
	"github.com/recscse/ctxd/internal/ui"
)

// RunDoctor performs diagnostic checks on the repository, index database, and AI agent configurations.
func RunDoctor(targetDir string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	ui.Header("🩺 ctxd Doctor - Health & Configuration Diagnostics")
	fmt.Println()
	fmt.Printf("  %s %s\n", ui.Dim.Sprint("Repository:"), ui.Bold.Sprint(absDir))
	fmt.Printf("  %s %s (%s/%s)\n\n", ui.Dim.Sprint("Environment:"), runtime.Version(), runtime.GOOS, runtime.GOARCH)

	allPassed := true

	// 1. Check Git Repository
	gitDir := filepath.Join(absDir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		fmt.Printf("  %s Git repository detected (.git present)\n", ui.GreenBold.Sprint("✓"))
	} else {
		fmt.Printf("  %s Not a Git repository (optional)\n", ui.Yellow.Sprint("!"))
	}

	// 2. Check Git executable
	if _, err := exec.LookPath("git"); err == nil {
		fmt.Printf("  %s Git executable found on system PATH\n", ui.GreenBold.Sprint("✓"))
	} else {
		fmt.Printf("  %s Git CLI not found on PATH (git features may be limited)\n", ui.Yellow.Sprint("!"))
	}

	// 3. Check .ctxd folder & index database
	dbPath := filepath.Join(absDir, ".ctxd", "index.db")
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		fmt.Printf("  %s Index database missing (Run 'ctxd init' to initialize)\n", ui.Red.Sprint("✗"))
		allPassed = false
	} else {
		fmt.Printf("  %s Index database found: %s (%s)\n",
			ui.GreenBold.Sprint("✓"),
			ui.Dim.Sprint(dbPath),
			formatBytes(dbInfo.Size()))

		// Check SQLite Integrity & Schema
		database, err := db.Open(dbPath)
		if err != nil {
			fmt.Printf("  %s Failed to open index database: %v\n", ui.Red.Sprint("✗"), err)
			allPassed = false
		} else {
			defer database.Close()
			ctx := context.Background()

			var integrity string
			row := database.QueryRowContext(ctx, "PRAGMA integrity_check;")
			if err := row.Scan(&integrity); err != nil || integrity != "ok" {
				fmt.Printf("  %s Database integrity check failed: %s\n", ui.Red.Sprint("✗"), integrity)
				allPassed = false
			} else {
				fmt.Printf("  %s SQLite database integrity verified (PRAGMA integrity_check: ok)\n", ui.GreenBold.Sprint("✓"))
			}

			ver, _ := db.GetSchemaVersion(database)
			fmt.Printf("  %s Schema version: v%d (latest: v%d)\n",
				ui.GreenBold.Sprint("✓"),
				ver,
				db.CurrentSchemaVersion)

			files, _ := db.GetAllFiles(ctx, database)
			syms, _ := db.GetAllSymbols(ctx, database)
			fmt.Printf("  %s Index contents: %d files, %d declared symbols\n",
				ui.GreenBold.Sprint("✓"),
				len(files),
				len(syms))
		}
	}

	// 4. Check MCP Client Configs
	homeDir, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = filepath.Join(homeDir, "AppData", "Roaming")
	}

	agentConfigs := []struct {
		name string
		path string
	}{
		{"Google Antigravity", filepath.Join(homeDir, ".gemini", "config", "mcp_config.json")},
		{"Claude Desktop", filepath.Join(appData, "Claude", "claude_desktop_config.json")},
		{"Cursor IDE", filepath.Join(homeDir, ".cursor", "mcp.json")},
		{"VS Code / Continue", filepath.Join(homeDir, ".continue", "config.json")},
	}

	fmt.Println()
	ui.Bold.Println("AI Client MCP Configurations:")
	for _, ac := range agentConfigs {
		if _, err := os.Stat(ac.path); err == nil {
			fmt.Printf("  %s %s: %s\n", ui.GreenBold.Sprint("✓"), ac.name, ui.Dim.Sprint(ac.path))
		} else {
			fmt.Printf("  %s %s: not configured (Run 'ctxd setup')\n", ui.Dim.Sprint("○"), ac.name)
		}
	}

	fmt.Println()
	if allPassed {
		ui.Success("🎉 All diagnostic checks passed! ctxd is healthy and ready.")
	} else {
		ui.Warning("Some issues were detected. Follow recommendations above or run 'ctxd setup' / 'ctxd init'.")
	}

	return nil
}
