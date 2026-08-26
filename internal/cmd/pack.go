// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/recscse/devctx/internal/db"
	"github.com/recscse/devctx/internal/symbols"
	"github.com/recscse/devctx/internal/ui"
)

// RunPack creates a high-density, token-optimized context bundle for a task or query.
func RunPack(targetDir string, query string) error {
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("pack query cannot be empty")
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".devctx", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("repository is not initialized (no index found at %s). Run 'ctxd init' first", dbPath)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	ctx := context.Background()
	packMarkdown, err := PackFeatureContext(ctx, database, absDir, query)
	if err != nil {
		return err
	}

	fmt.Println(packMarkdown)
	return nil
}

// PackFeatureContext bundles related types, functions, callers, and test files for a topic into 1 high-density markdown bundle.
func PackFeatureContext(ctx context.Context, database *sql.DB, rootDir string, topic string) (string, error) {
	// 1. Find matching declared symbols
	syms, err := db.FindSymbols(ctx, database, topic)
	if err != nil {
		return "", err
	}

	// 2. Find matching code snippets
	ftsMatches, _ := db.SearchFTS(ctx, database, topic, 15)

	// 3. Find test suites
	tests, _ := db.FindTestsFor(ctx, database, topic)

	// 4. Find stored architecture decisions
	decisions, _ := db.GetDecisions(ctx, database, topic)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 📦 Feature Context Pack: `%s`\n\n", topic))

	// Section A: Architecture Decisions
	if len(decisions) > 0 {
		sb.WriteString("## 🧠 Stored Architectural Decisions\n")
		for _, d := range decisions {
			sb.WriteString(fmt.Sprintf("- **[%s]** %s *(recorded %s)*\n",
				d.Topic, d.Summary, d.CreatedAt.Format("2006-01-02")))
		}
		sb.WriteString("\n")
	}

	// Section B: Declared Symbols & Types
	if len(syms) > 0 {
		sb.WriteString("## 🧬 Core Types & Functions\n")
		for i, s := range syms {
			if i >= 12 {
				sb.WriteString(fmt.Sprintf("... +%d other symbol definitions\n", len(syms)-12))
				break
			}
			sb.WriteString(fmt.Sprintf("- `[%s]` **%s** (`%s:%d`)\n  `%s`\n",
				s.Kind, s.Name, s.FilePath, s.LineNumber, s.Signature))
		}
		sb.WriteString("\n")
	}

	// Section C: Matching Test Files
	if len(tests) > 0 {
		sb.WriteString("## 🧪 Relevant Test Suites\n")
		for _, t := range tests {
			sb.WriteString(fmt.Sprintf("- **%s**\n", t.TestFilePath))
			for _, name := range t.TestNames {
				sb.WriteString(fmt.Sprintf("   • `%s`\n", name))
			}
		}
		sb.WriteString("\n")
	}

	// Section D: File Skeletons for top related files
	fileSet := make(map[string]bool)
	for _, s := range syms {
		fileSet[s.FilePath] = true
	}
	for _, m := range ftsMatches {
		fileSet[m.Path] = true
	}

	var candidateFiles []string
	for f := range fileSet {
		if !strings.Contains(f, "_test.") && !strings.Contains(f, ".spec.") && !strings.Contains(f, ".test.") {
			candidateFiles = append(candidateFiles, f)
		}
	}
	sort.Strings(candidateFiles)

	if len(candidateFiles) > 0 {
		sb.WriteString("## 📄 Structural File Skeletons\n\n")
		maxSkeletons := 3
		for i, f := range candidateFiles {
			if i >= maxSkeletons {
				break
			}
			fullPath := filepath.Join(rootDir, filepath.FromSlash(f))
			content, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			fileSyms, _ := db.FindSymbols(ctx, database, f)
			skel := symbols.GenerateSkeleton(f, "auto", content, fileSyms)
			sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", skel))
		}
	}

	sb.WriteString("> *Tip: Use `devctx:read_file_context` only on specific lines if you need the full implementation details.*")
	return strings.TrimSpace(sb.String()), nil
}

// RunDecisions lists stored architectural decisions.
func RunDecisions(targetDir string, topic string, asJSON bool) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".devctx", "index.db")
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	ctx := context.Background()
	decisions, err := db.GetDecisions(ctx, database, topic)
	if err != nil {
		return fmt.Errorf("failed to get decisions: %w", err)
	}

	if asJSON {
		return ui.PrintJSON(decisions)
	}

	if len(decisions) == 0 {
		ui.Warning(fmt.Sprintf("No architectural decisions recorded for topic '%s'", topic))
		return nil
	}

	ui.CyanBold.Printf("🧠 Stored Architectural Decisions (%d found):\n\n", len(decisions))
	for i, d := range decisions {
		fmt.Printf("%d. [%s] %s\n", i+1, ui.Yellow.Sprint(d.Topic), ui.Bold.Sprint(d.Summary))
		fmt.Printf("   %s %s\n\n", ui.Dim.Sprint("Recorded:"), d.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
	}

	return nil
}
