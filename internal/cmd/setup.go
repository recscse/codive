// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/recscse/devctx/internal/ui"
)

// MCPConfigFile represents standard MCP JSON configurations.
type MCPConfigFile struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPServerConfig represents an individual server entry.
type MCPServerConfig struct {
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	Disabled    bool     `json:"disabled,omitempty"`
	AutoApprove []string `json:"autoApprove,omitempty"`
}

type agentTarget struct {
	Name       string
	PathGetter func() (string, error)
}

const ctxdAgentRule = `## Code Search & Exploration Rules
- **DO NOT** use raw ` + "`grep`" + `, ` + "`ripgrep`" + `, or recursive ` + "`list_dir`" + ` for codebase exploration.
- **ALWAYS PREFER** the ` + "`ctxd`" + ` MCP tools:
  1. Use ` + "`devctx:get_repo_map`" + ` to understand the codebase structure and symbols.
  2. Use ` + "`devctx:find_symbol`" + ` when locating function, class, or type definitions.
  3. Use ` + "`devctx:find_references`" + ` when discovering callers or usages of a function/type.
  4. Use ` + "`devctx:search_code`" + ` when searching for terms or strings across files.
  5. Use ` + "`devctx:read_file_context`" + ` to read files with AST symbol summaries.
`

// RunSetup automatically detects installed AI tools, configures ctxd MCP server entries, and installs agent prioritization rules.
func RunSetup(targetDir string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine ctxd executable path: %w", err)
	}
	exePath, _ = filepath.Abs(exePath)

	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		absTarget = "."
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to find user home directory: %w", err)
	}

	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = filepath.Join(homeDir, "AppData", "Roaming")
	}

	codeStorage := filepath.Join(appData, "Code", "User", "globalStorage")
	if runtime.GOOS == "darwin" {
		codeStorage = filepath.Join(homeDir, "Library", "Application Support", "Code", "User", "globalStorage")
	} else if runtime.GOOS == "linux" {
		codeStorage = filepath.Join(homeDir, ".config", "Code", "User", "globalStorage")
	}

	targets := []agentTarget{
		{
			Name: "Google Antigravity",
			PathGetter: func() (string, error) {
				return filepath.Join(homeDir, ".gemini", "config", "mcp_config.json"), nil
			},
		},
		{
			Name: "Claude Desktop",
			PathGetter: func() (string, error) {
				if runtime.GOOS == "windows" {
					return filepath.Join(appData, "Claude", "claude_desktop_config.json"), nil
				}
				if runtime.GOOS == "darwin" {
					return filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
				}
				return filepath.Join(homeDir, ".config", "Claude", "claude_desktop_config.json"), nil
			},
		},
		{
			Name: "Cursor IDE",
			PathGetter: func() (string, error) {
				return filepath.Join(homeDir, ".cursor", "mcp.json"), nil
			},
		},
		{
			Name: "Windsurf",
			PathGetter: func() (string, error) {
				return filepath.Join(homeDir, ".codeium", "windsurf", "mcp_config.json"), nil
			},
		},
		{
			Name: "VS Code / Continue",
			PathGetter: func() (string, error) {
				return filepath.Join(homeDir, ".continue", "config.json"), nil
			},
		},
		{
			Name: "VS Code / Cline Extension",
			PathGetter: func() (string, error) {
				return filepath.Join(codeStorage, "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"), nil
			},
		},
		{
			Name: "VS Code / Roo Code Extension",
			PathGetter: func() (string, error) {
				return filepath.Join(codeStorage, "rooveterinaryinc.roo-cline", "settings", "cline_mcp_settings.json"), nil
			},
		},
		{
			Name: "VS Code / Workspace (.vscode/mcp.json)",
			PathGetter: func() (string, error) {
				return filepath.Join(absTarget, ".vscode", "mcp.json"), nil
			},
		},
	}

	ui.Header("devctx — AI Client MCP Configuration Setup")
	ui.Divider()
	ui.KeyValue("Executable", exePath)
	ui.KeyValue("Target Repo", absTarget)
	ui.Divider()
	fmt.Println()

	configuredCount := 0

	// 1. Configure MCP server JSON configs
	for _, t := range targets {
		cfgPath, err := t.PathGetter()
		if err != nil || cfgPath == "" {
			continue
		}

		if err := writeOrMergeMCPConfig(cfgPath, exePath, absTarget); err != nil {
			ui.Warning(fmt.Sprintf("Could not configure %s (%v)", t.Name, err))
			continue
		}

		fmt.Printf("  %s %s (%s)\n",
			ui.GreenBold.Sprint("✔"),
			ui.Bold.Sprint(t.Name),
			ui.Dim.Sprint(cfgPath))
		configuredCount++
	}

	// 2. Install agent prioritization rules
	rulePaths := []string{
		filepath.Join(homeDir, ".gemini", "antigravity", "rules", "devctx.md"),
		filepath.Join(homeDir, ".gemini", "rules", "devctx.md"),
		filepath.Join(absTarget, ".gemini", "rules", "devctx.md"),
		filepath.Join(absTarget, ".cursorrules"),
		filepath.Join(absTarget, ".windsurfrules"),
		filepath.Join(absTarget, "CLAUDE.md"),
		filepath.Join(absTarget, "GEMINI.md"),
	}

	for _, rp := range rulePaths {
		if err := writeAgentRule(rp); err == nil {
			fmt.Printf("  %s %s\n",
				ui.GreenBold.Sprint("✔ Rule Installed:"),
				ui.Dim.Sprint(rp))
		}
	}

	fmt.Println()
	if configuredCount > 0 {
		ui.Success("devctx is now fully registered across all AI coding assistants!")
		fmt.Println("  AI agents will now automatically call devctx MCP tools for symbol search and context packing.")
	} else {
		ui.Warning("No supported AI agent configuration directories were found.")
	}

	return nil
}

func writeOrMergeMCPConfig(cfgPath string, exePath string, repoDir string) error {
	dir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	config := MCPConfigFile{
		MCPServers: make(map[string]MCPServerConfig),
	}

	// Read existing config if present
	if data, err := os.ReadFile(cfgPath); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &config)
		if config.MCPServers == nil {
			config.MCPServers = make(map[string]MCPServerConfig)
		}
	}

	// Set/Update ctxd with autoApprove enabled
	config.MCPServers["devctx"] = MCPServerConfig{
		Command: exePath,
		Args:    []string{"serve", repoDir},
		AutoApprove: []string{
			"get_repo_map",
			"find_symbol",
			"find_references",
			"search_code",
			"read_file_context",
		},
	}

	bytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cfgPath, bytes, 0644)
}

func writeAgentRule(rulePath string) error {
	dir := filepath.Dir(rulePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// If file exists and already contains ctxd rule, skip
	if existing, err := os.ReadFile(rulePath); err == nil {
		if strings.Contains(string(existing), "devctx") {
			return nil
		}
		// Append rule
		newContent := string(existing) + "\n\n" + ctxdAgentRule
		return os.WriteFile(rulePath, []byte(newContent), 0644)
	}

	return os.WriteFile(rulePath, []byte(ctxdAgentRule), 0644)
}
