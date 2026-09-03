// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"fmt"

	"github.com/recscse/codive/internal/ui"
)

// RunAbout displays project information, author, and architectural summary.
func RunAbout(version, buildDate, gitCommit string) error {
	fmt.Println()
	fmt.Printf("  %s  %s\n\n",
		ui.GreenBold.Sprint("codive"),
		ui.Dim.Sprint("Local-first context engine for AI coding agents"),
	)

	ui.KeyValue("Version", version)
	ui.KeyValue("Build Date", buildDate)
	ui.KeyValue("Git Commit", gitCommit)
	fmt.Println()
	ui.KeyValue("Author", "Brijesh Yadav")
	ui.KeyValue("License", "MIT")
	ui.KeyValue("Repository", "https://github.com/recscse/codive")
	ui.KeyValue("Website", "https://recscse.github.io/codive/")
	ui.KeyValue("Docs", "https://recscse.github.io/codive/docs.html")
	fmt.Println()
	ui.Divider()
	fmt.Println()
	fmt.Printf("  %s\n\n",
		"codive indexes Abstract Syntax Trees (AST) into an embedded SQLite WAL",
	)
	fmt.Printf("  %s\n",
		"database and exposes 14 MCP tools for AI coding agents. Symbol lookups,",
	)
	fmt.Printf("  %s\n\n",
		"full-text search, blast radius analysis, and feature context packs run in",
	)
	fmt.Printf("  %s\n\n",
		"< 2ms with no cloud dependencies.",
	)
	ui.Success("100% local-first · zero telemetry · MIT licensed")
	fmt.Println()
	return nil
}
