// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/recscse/devctx/internal/ui"
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

// RunInitRules inspects the repository, detects the technology stack, and auto-generates tailor-made agent architecture rules.
func RunInitRules(targetDir string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	ui.Header("✨ Auto-Generating Agent Architecture Rules")
	fmt.Println()

	stack := detectProjectStack(absDir)
	fmt.Printf("  %s %s\n", ui.Dim.Sprint("Detected Language:"), ui.Bold.Sprint(stack.Language))
	if stack.Framework != "" {
		fmt.Printf("  %s %s\n", ui.Dim.Sprint("Detected Framework:"), ui.CyanBold.Sprint(stack.Framework))
	}
	fmt.Printf("  %s %s\n", ui.Dim.Sprint("Test Command:"), ui.GreenBold.Sprint(stack.TestCommand))
	fmt.Println()

	rulesContent := generateRulesMarkdown(stack)

	ruleTargets := []string{
		filepath.Join(absDir, "AGENTS.md"),
		filepath.Join(absDir, "CLAUDE.md"),
		filepath.Join(absDir, "GEMINI.md"),
		filepath.Join(absDir, ".cursorrules"),
	}

	for _, target := range ruleTargets {
		if err := os.WriteFile(target, []byte(rulesContent), 0644); err != nil {
			ui.Warning(fmt.Sprintf("Could not write %s: %v", filepath.Base(target), err))
		} else {
			fmt.Printf("  %s %s\n", ui.GreenBold.Sprint("✓ Created Rule:"), ui.Bold.Sprint(filepath.Base(target)))
		}
	}

	fmt.Println()
	ui.Success("🎉 Custom architecture rules generated! AI agents will automatically follow these guidelines.")
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

func generateRulesMarkdown(stack ProjectStack) string {
	var sb strings.Builder
	sb.WriteString("# AI Agent Architecture & Exploration Guidelines\n\n")

	sb.WriteString("## Project Overview\n")
	sb.WriteString(fmt.Sprintf("- **Primary Language**: %s\n", stack.Language))
	if stack.Framework != "" {
		sb.WriteString(fmt.Sprintf("- **Framework**: %s\n", stack.Framework))
	}
	if stack.BuildCommand != "" {
		sb.WriteString(fmt.Sprintf("- **Build Command**: `%s`\n", stack.BuildCommand))
	}
	if stack.TestCommand != "" {
		sb.WriteString(fmt.Sprintf("- **Test Command**: `%s`\n", stack.TestCommand))
	}
	if stack.LintCommand != "" {
		sb.WriteString(fmt.Sprintf("- **Lint Command**: `%s`\n\n", stack.LintCommand))
	} else {
		sb.WriteString("\n")
	}

	sb.WriteString("## Code Search & Exploration Rules\n")
	sb.WriteString("- **DO NOT** use raw `grep`, `ripgrep`, or recursive `list_dir` for codebase exploration.\n")
	sb.WriteString("- **ALWAYS PREFER** the `devctx` MCP tools for zero-token code discovery:\n")
	sb.WriteString("  1. Use `devctx:get_repo_map` to understand the codebase structure and symbols.\n")
	sb.WriteString("  2. Use `devctx:get_file_skeleton` to inspect file structure without dumping thousands of tokens.\n")
	sb.WriteString("  3. Use `devctx:find_symbol` when locating function, class, or type definitions.\n")
	sb.WriteString("  4. Use `devctx:find_callers` or `devctx:find_references` when discovering usages or refactoring.\n")
	sb.WriteString("  5. Use `devctx:find_tests_for` to locate corresponding unit test suites before and after making changes.\n")
	sb.WriteString("  6. Use `devctx:pack_feature_context` to bundle complete feature entrypoints in 1 single turn.\n")
	sb.WriteString("  7. Use `devctx:get_git_changes` to review uncommitted AST changes without noisy unified diffs.\n\n")

	sb.WriteString("## Engineering Conventions\n")
	sb.WriteString("1. Always run tests using the verified test command after making edits.\n")
	sb.WriteString("2. Avoid breaking existing exported signatures without updating all callers.\n")
	sb.WriteString("3. Record significant architectural decisions using `devctx:save_decision`.\n")

	return sb.String()
}
