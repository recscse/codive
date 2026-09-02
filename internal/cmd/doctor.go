// Package cmd implements the command line actions and subcommands for devctx.
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/recscse/devctx/internal/db"
	"github.com/recscse/devctx/internal/ui"
)

// RunDoctor performs diagnostic checks on the repository, index, and AI agent configurations.
func RunDoctor(targetDir string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	ui.SectionHeader("Doctor — Index & Agent Health Check")
	ui.KeyValue("Repository", absDir)
	ui.KeyValue("Go Runtime", fmt.Sprintf("%s  %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH))
	fmt.Println()

	allPassed := true

	// 1. Git repository
	if _, err := os.Stat(filepath.Join(absDir, ".git")); err == nil {
		ui.CheckPass("Git repository detected (.git present)")
	} else {
		ui.CheckWarn("Not a git repository — git features will be limited")
	}

	// 2. Git executable
	if _, err := exec.LookPath("git"); err == nil {
		ui.CheckPass("git executable found on PATH")
	} else {
		ui.CheckWarn("git not found on PATH — git diff and hooks will be unavailable")
	}

	// 3. Index database
	dbPath := filepath.Join(absDir, ".devctx", "index.db")
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		ui.CheckFail(fmt.Sprintf("Index not found at %s — run 'devctx init'", dbPath))
		allPassed = false
	} else {
		ui.CheckPass(fmt.Sprintf("Index database found  (%s)", formatBytes(dbInfo.Size())))

		database, err := db.Open(dbPath)
		if err != nil {
			ui.CheckFail(fmt.Sprintf("Failed to open index: %v", err))
			allPassed = false
		} else {
			defer database.Close()
			ctx := context.Background()
			stats, err := db.GetStats(ctx, database)
			if err != nil {
				ui.CheckFail(fmt.Sprintf("Index query failed: %v", err))
				allPassed = false
			} else {
				ui.CheckPass(fmt.Sprintf("Index integrity OK  (%d files, %d symbols)", stats.TotalFiles, stats.TotalFiles))
			}
		}
	}

	// 4. Git hooks
	for _, hook := range []string{"post-commit", "post-checkout"} {
		hookPath := filepath.Join(absDir, ".git", "hooks", hook)
		if _, err := os.Stat(hookPath); err == nil {
			ui.CheckPass(fmt.Sprintf("git hook installed: %s", hook))
		} else {
			ui.CheckWarn(fmt.Sprintf("git hook missing: %s  (run 'devctx install-hooks')", hook))
		}
	}

	// 5. AI agent MCP configs
	fmt.Println()
	ui.Label("AI Agent MCP Configurations")

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

	for _, ac := range agentConfigs {
		if _, err := os.Stat(ac.path); err == nil {
			ui.CheckPass(fmt.Sprintf("%-24s  %s", ac.name, ui.Dim.Sprint(ac.path)))
		} else {
			ui.ListItem(ac.name, "not configured — run 'devctx setup'")
		}
	}

	fmt.Println()
	if allPassed {
		ui.Success("All checks passed — devctx is healthy and ready.")
	} else {
		ui.Warning("Some checks failed — follow the recommendations above.")
	}
	fmt.Println()
	return nil
}
