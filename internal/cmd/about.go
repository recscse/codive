// Package cmd implements the command line actions and subcommands for devctx.
package cmd

import (
	"fmt"

	"github.com/recscse/devctx/internal/ui"
)

// RunAbout displays detailed project information, author, license, and architectural links.
func RunAbout(version, buildDate, gitCommit string) error {
	ui.Header("devctx — Developer Context Engine for AI Coding Agents")
	ui.Divider()
	ui.KeyValueHighlight("Version", version)
	ui.KeyValue("Author", "Brijesh Yadav (https://recscse.github.io)")
	ui.KeyValue("License", "MIT Open Source License")
	ui.KeyValue("Repository", "https://github.com/recscse/devctx")
	ui.KeyValue("Website", "https://recscse.github.io/devctx/")
	ui.KeyValue("Documentation", "https://recscse.github.io/devctx/docs.html")
	ui.KeyValue("Engineering Blog", "https://recscse.github.io/devctx/blog.html")
	ui.KeyValue("Build Date", buildDate)
	ui.KeyValue("Git Commit", gitCommit)
	ui.Divider()
	fmt.Println()
	fmt.Println("  devctx indexes Abstract Syntax Trees (AST) into an embedded SQLite WAL database,")
	fmt.Println("  providing sub-millisecond symbol search, code skeletons, and PR blast radius")
	fmt.Println("  analysis to Claude Code, Cursor, Google Antigravity, and VS Code over MCP.")
	fmt.Println()
	ui.Success("100% Local-First • Zero Cloud Telemetry • MIT Licensed")
	fmt.Println()

	return nil
}
