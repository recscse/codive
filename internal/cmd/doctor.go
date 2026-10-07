// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/ui"
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
	dbPath := filepath.Join(absDir, ".codive", "index.db")
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		ui.CheckFail(fmt.Sprintf("Index not found at %s — run 'codive init'", dbPath))
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
				allSyms, symErr := db.GetAllSymbols(ctx, database)
				if symErr != nil {
					ui.CheckFail(fmt.Sprintf("Symbol query failed: %v", symErr))
					allPassed = false
				} else {
					ui.CheckPass(fmt.Sprintf("Index integrity OK  (%s, %s)", ui.Count(stats.TotalFiles, "file", "files"), ui.Count(len(allSyms), "symbol", "symbols")))
				}
			}
		}
	}

	// 4. Git hooks
	for _, hook := range []string{"post-commit", "post-checkout"} {
		hookPath := filepath.Join(absDir, ".git", "hooks", hook)
		if _, err := os.Stat(hookPath); err == nil {
			ui.CheckPass(fmt.Sprintf("git hook installed: %s", hook))
		} else {
			ui.CheckWarn(fmt.Sprintf("git hook missing: %s  (run 'codive install-hooks')", hook))
		}
	}

	// 5. AI agent MCP configs
	fmt.Println()
	ui.Label("AI Agent MCP Configurations")

	// Same client list and paths `codive setup` uses, checking that codive's
	// entry is actually present rather than just that a config file exists.
	if env, err := defaultSetupEnv(absDir); err == nil {
		detected := 0
		for _, t := range setupTargets(env) {
			if !t.Detected {
				continue
			}
			detected++
			if isRegistered(t) {
				ui.CheckPass(fmt.Sprintf("%-28s  %s", t.Client, ui.Dim.Sprint(t.Path)))
			} else {
				ui.ListItem(t.Client, "installed but codive is not registered — run 'codive setup'")
			}
		}
		if detected == 0 {
			ui.ListItem("AI clients", "none detected on this machine")
		}
	}

	fmt.Println()
	if allPassed {
		ui.Success("All checks passed — codive is healthy and ready.")
	} else {
		ui.Warning("Some checks failed — follow the recommendations above.")
	}
	fmt.Println()
	return nil
}
