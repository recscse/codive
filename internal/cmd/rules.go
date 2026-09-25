// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/recscse/codive/internal/ui"
)

// ProjectStack contains detected tech stack metadata.
type ProjectStack struct {
	Language       string
	Framework      string
	PackageManager string
	BuildCommand   string
	TestCommand    string
	LintCommand    string
}

// RunInitRules detects the project's stack and writes agent guidance
// (project overview plus codive usage) into the agent instruction files in
// use. It only ever edits codive's own marked block, so existing
// instructions are preserved; undo removes the block again.
func RunInitRules(targetDir string, dryRun, undo bool) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}
	env, err := defaultSetupEnv(absDir)
	if err != nil {
		return err
	}

	ui.Header("✨ Agent Architecture Rules")
	fmt.Println()

	stack := detectProjectStack(absDir)
	if !undo {
		fmt.Printf("  %s %s\n", ui.Dim.Sprint("Detected Language:"), ui.Bold.Sprint(stack.Language))
		if stack.Framework != "" {
			fmt.Printf("  %s %s\n", ui.Dim.Sprint("Detected Framework:"), ui.CyanBold.Sprint(stack.Framework))
		}
		fmt.Printf("  %s %s\n", ui.Dim.Sprint("Test Command:"), ui.GreenBold.Sprint(stack.TestCommand))
		fmt.Println()
	}

	actions := writeAgentGuidance(absDir, env, generateRulesMarkdown(stack), dryRun, undo)
	for _, a := range actions {
		fmt.Printf("  %s %s\n", ui.GreenBold.Sprint("✓ "+a.Status+":"), ui.Bold.Sprint(a.Path))
	}
	if len(actions) == 0 {
		ui.Warning("Nothing to change.")
	}
	fmt.Println()
	return nil
}

func detectProjectStack(rootDir string) ProjectStack {
	stack := ProjectStack{
		Language:       "Generic",
		PackageManager: "N/A",
		TestCommand:    "N/A",
	}

	// 1. Check Go
	if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err == nil {
		stack.Language = "Go"
		stack.PackageManager = "go modules"
		stack.BuildCommand = "go build ./..."
		stack.TestCommand = "go test -count=1 -v ./..."
		stack.LintCommand = "go vet ./..."
		return stack
	}

	// 2. Check Node / TypeScript / JavaScript
	pkgPath := filepath.Join(rootDir, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		stack.Language = "TypeScript / JavaScript"
		stack.PackageManager = "npm"
		if _, err := os.Stat(filepath.Join(rootDir, "pnpm-lock.yaml")); err == nil {
			stack.PackageManager = "pnpm"
		} else if _, err := os.Stat(filepath.Join(rootDir, "yarn.lock")); err == nil {
			stack.PackageManager = "yarn"
		}

		content := string(data)
		if strings.Contains(content, "\"next\"") {
			stack.Framework = "Next.js"
		} else if strings.Contains(content, "\"vite\"") {
			stack.Framework = "React (Vite)"
		} else if strings.Contains(content, "\"express\"") {
			stack.Framework = "Express"
		}

		stack.BuildCommand = stack.PackageManager + " run build"
		stack.TestCommand = stack.PackageManager + " test"
		stack.LintCommand = stack.PackageManager + " run lint"
		return stack
	}

	// 3. Check Python
	if _, err := os.Stat(filepath.Join(rootDir, "pyproject.toml")); err == nil || fileExists(rootDir, "requirements.txt") {
		stack.Language = "Python"
		stack.PackageManager = "pip"
		stack.TestCommand = "pytest"
		stack.LintCommand = "flake8"

		if fileExists(rootDir, "main.py") {
			mainData, _ := os.ReadFile(filepath.Join(rootDir, "main.py"))
			if strings.Contains(string(mainData), "FastAPI") {
				stack.Framework = "FastAPI"
			} else if strings.Contains(string(mainData), "Flask") {
				stack.Framework = "Flask"
			}
		}
		return stack
	}

	// 4. Check Rust
	if _, err := os.Stat(filepath.Join(rootDir, "Cargo.toml")); err == nil {
		stack.Language = "Rust"
		stack.PackageManager = "cargo"
		stack.BuildCommand = "cargo build"
		stack.TestCommand = "cargo test"
		stack.LintCommand = "cargo clippy"
		return stack
	}

	return stack
}

func fileExists(dir string, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// generateRulesMarkdown renders the project overview followed by the shared
// codive usage guidance.
func generateRulesMarkdown(stack ProjectStack) string {
	var sb strings.Builder
	sb.WriteString("## Project Overview\n")
	sb.WriteString(fmt.Sprintf("- **Primary Language**: %s\n", stack.Language))
	if stack.Framework != "" {
		sb.WriteString(fmt.Sprintf("- **Framework**: %s\n", stack.Framework))
	}
	if stack.BuildCommand != "" {
		sb.WriteString(fmt.Sprintf("- **Build Command**: `%s`\n", stack.BuildCommand))
	}
	if stack.TestCommand != "" && stack.TestCommand != "N/A" {
		sb.WriteString(fmt.Sprintf("- **Test Command**: `%s`\n", stack.TestCommand))
	}
	if stack.LintCommand != "" {
		sb.WriteString(fmt.Sprintf("- **Lint Command**: `%s`\n", stack.LintCommand))
	}
	sb.WriteString("\n")
	sb.WriteString(codiveGuidance)
	return sb.String()
}

// codiveGuidance tells agents when to reach for which codive tool. It maps
// questions to tools rather than banning grep: plain text search is still
// the right tool for literal strings and for files codive can't parse.
const codiveGuidance = "## Code navigation with codive (MCP)\n" +
	"Prefer codive's MCP tools to understand code: they answer from a live index of this repository and return exact, compact results instead of whole files.\n" +
	"- Where is X defined? → `find_symbol`\n" +
	"- Show me the code of X → `read_symbol` (just that function or type, not the whole file)\n" +
	"- What's in this file? → `get_file_skeleton`, then `read_file_context` with `start_line`/`end_line` for just the part you need\n" +
	"- Who calls X? What breaks if X changes? → `find_callers`, `blast_radius`\n" +
	"- Which tests cover this? → `find_tests_for`\n" +
	"- What have I changed so far? → `get_git_changes`\n" +
	"- Overview of a feature in one call → `pack_feature_context`\n" +
	"\n" +
	"Results marked \"MORE EXIST\" or \"showing N of M\" are partial: raise `limit` or narrow the query before drawing conclusions.\n" +
	"Plain text search (grep/rg) is still the right tool for literal strings, log or error messages, config values, and files codive doesn't parse.\n" +
	"Record lasting design decisions with `save_decision` so later sessions can find them.\n"
