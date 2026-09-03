// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/symbols"
	"github.com/recscse/codive/internal/ui"
)

// isASTCapableLanguage reports whether symbols.ExtractSymbols has a real parser
// for this language. Anything else always falls through to extractGenericSymbols,
// which returns no symbols, so a skeleton for it is always the empty fallback.
func isASTCapableLanguage(lang string) bool {
	switch lang {
	case "Go", "Python", "TypeScript", "JavaScript", "Java", "C#", "Rust":
		return true
	default:
		return false
	}
}

// RunPack creates a high-density, token-optimized context bundle for a task or query.
func RunPack(targetDir string, query string) error {
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("pack query cannot be empty")
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".codive", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("repository is not initialized (no index found at %s). Run 'codive init' first", dbPath)
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
	sb.WriteString(fmt.Sprintf("# Feature Context Pack: `%s`\n\n", topic))

	// Section A: Architecture Decisions
	if len(decisions) > 0 {
		sb.WriteString("## Stored Architectural Decisions\n")
		for _, d := range decisions {
			sb.WriteString(fmt.Sprintf("- **[%s]** %s *(recorded %s)*\n",
				d.Topic, d.Summary, d.CreatedAt.Format("2006-01-02")))
		}
		sb.WriteString("\n")
	}

	// Section B: Declared Symbols & Types
	if len(syms) > 0 {
		sb.WriteString("## Core Types & Functions\n")
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
		sb.WriteString("## Relevant Test Suites\n")
		for _, t := range tests {
			sb.WriteString(fmt.Sprintf("- **%s**\n", t.TestFilePath))
			for _, name := range t.TestNames {
				sb.WriteString(fmt.Sprintf("   • `%s`\n", name))
			}
		}
		sb.WriteString("\n")
	}

	// Section D: File Skeletons for top related files.
	// Rank by relevance rather than alphabetically: a file whose declared symbols
	// matched the topic is a far stronger signal than a file that merely mentions
	// the topic word in prose, and among FTS-only matches we keep SearchFTS's own
	// bm25 rank order (best match first) instead of discarding it.
	seenFile := make(map[string]bool)
	var candidateFiles []string
	addCandidate := func(f string) {
		if seenFile[f] {
			return
		}
		if strings.Contains(f, "_test.") || strings.Contains(f, ".spec.") || strings.Contains(f, ".test.") {
			return
		}
		seenFile[f] = true
		candidateFiles = append(candidateFiles, f)
	}
	for _, s := range syms {
		addCandidate(s.FilePath)
	}
	for _, m := range ftsMatches {
		addCandidate(m.Path)
	}

	// A skeleton is inherently useless for a file with no AST extractor, so prefer
	// AST-capable source files for the limited skeleton slots over docs/markup that
	// merely mention the topic more densely and out-rank the real source file in
	// FTS's bm25 score. Order is preserved within each group.
	var codeFiles, otherFiles []string
	for _, f := range candidateFiles {
		if isASTCapableLanguage(scanner.DetectLanguage(f)) {
			codeFiles = append(codeFiles, f)
		} else {
			otherFiles = append(otherFiles, f)
		}
	}
	candidateFiles = append(codeFiles, otherFiles...)

	if len(candidateFiles) > 0 {
		sb.WriteString("## Structural File Skeletons\n\n")
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
			// Pass nil symbols with the real detected language (not the literal
			// string "auto", which matched no case in ExtractSymbols' switch and
			// silently produced an empty skeleton for every file, always) so
			// GenerateSkeleton's own fallback re-extracts symbols correctly.
			lang := scanner.DetectLanguage(f)
			skel := symbols.GenerateSkeleton(f, lang, content, nil)
			sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", skel))
		}
	}

	sb.WriteString("> Tip: use `codive:read_file_context` only on specific lines if you need the full implementation details.")
	return strings.TrimSpace(sb.String()), nil
}

// RunDecisions lists stored architectural decisions.
func RunDecisions(targetDir string, topic string, asJSON bool) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".codive", "index.db")
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
