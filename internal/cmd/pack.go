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
	"github.com/recscse/codive/internal/ranker"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/symbols"
	"github.com/recscse/codive/internal/ui"
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

// PackFeatureContext bundles related types, functions, callers, and test files for a topic into 1 high-density markdown bundle
// structured around Karpathy's Attention U-Curve (interfaces at top, target code & tests at bottom).
func PackFeatureContext(ctx context.Context, database *sql.DB, rootDir string, topic string) (string, error) {
	topicTerms := strings.Fields(topic)
	expanded := ranker.ExpandTerms(topicTerms)

	// 1. Find matching declared symbols (direct + expanded intent synonyms)
	syms, err := db.FindSymbols(ctx, database, topic)
	if err != nil {
		return "", err
	}

	seenSym := make(map[string]bool)
	var allSyms []db.SymbolRecord
	for _, s := range syms {
		key := fmt.Sprintf("%s:%d", s.FilePath, s.LineNumber)
		if !seenSym[key] {
			seenSym[key] = true
			allSyms = append(allSyms, s)
		}
	}

	if len(allSyms) < 12 {
		for _, expTerm := range expanded {
			if strings.EqualFold(expTerm, topic) {
				continue
			}
			moreSyms, _ := db.FindSymbols(ctx, database, expTerm)
			for _, s := range moreSyms {
				key := fmt.Sprintf("%s:%d", s.FilePath, s.LineNumber)
				if !seenSym[key] {
					seenSym[key] = true
					allSyms = append(allSyms, s)
					if len(allSyms) >= 16 {
						break
					}
				}
			}
			if len(allSyms) >= 16 {
				break
			}
		}
	}
	syms = allSyms

	// 2. Find matching code snippets
	ftsMatches, _ := db.SearchFTS(ctx, database, topic, 15)
	if len(ftsMatches) < 5 {
		for _, expTerm := range expanded {
			if strings.EqualFold(expTerm, topic) {
				continue
			}
			moreFTS, _ := db.SearchFTS(ctx, database, expTerm, 5)
			for _, m := range moreFTS {
				ftsMatches = append(ftsMatches, m)
			}
			if len(ftsMatches) >= 15 {
				break
			}
		}
	}

	// 3. Find test suites
	tests, _ := db.FindTestsFor(ctx, database, topic)
	if len(tests) == 0 {
		for _, expTerm := range expanded {
			if strings.EqualFold(expTerm, topic) {
				continue
			}
			moreTests, _ := db.FindTestsFor(ctx, database, expTerm)
			if len(moreTests) > 0 {
				tests = append(tests, moreTests...)
				break
			}
		}
	}

	// 4. Find stored architecture decisions
	decisions, _ := db.GetDecisions(ctx, database, topic)
	if len(decisions) == 0 {
		for _, expTerm := range expanded {
			if strings.EqualFold(expTerm, topic) {
				continue
			}
			moreDecs, _ := db.GetDecisions(ctx, database, expTerm)
			if len(moreDecs) > 0 {
				decisions = append(decisions, moreDecs...)
				break
			}
		}
	}

	// Partition symbols into Types/Interfaces (Priming zone) vs Functions/Methods (Recency zone)
	var typeSyms []db.SymbolRecord
	var fnSyms []db.SymbolRecord
	for _, s := range syms {
		if s.Kind == "struct" || s.Kind == "interface" || s.Kind == "class" || s.Kind == "type" {
			typeSyms = append(typeSyms, s)
		} else {
			fnSyms = append(fnSyms, s)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Feature Context Pack: `%s`\n\n", topic))

	// -------------------------------------------------------------
	// ZONE 1 (PRIMING ANCHOR): Decisions & Invariants + Core Types
	// -------------------------------------------------------------
	if len(decisions) > 0 {
		sb.WriteString("## Stored Architectural Decisions\n")
		for _, d := range decisions {
			sb.WriteString(fmt.Sprintf("- **[%s]** %s\n", d.Topic, d.Summary))
		}
		sb.WriteString("\n")
	}

	if len(typeSyms) > 0 {
		sb.WriteString("## Core Types & Interfaces\n")
		for i, s := range typeSyms {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("... +%d other type definitions\n", len(typeSyms)-10))
				break
			}
			sb.WriteString(fmt.Sprintf("- `[%s]` **%s** (`%s:%d`)\n  `%s`\n",
				s.Kind, s.Name, s.FilePath, s.LineNumber, s.Signature))
		}
		sb.WriteString("\n")
	}

	// -------------------------------------------------------------
	// ZONE 2 (MIDDLE BODY): Call Relationships + Structural Skeletons
	// -------------------------------------------------------------
	var relSyms []db.SymbolRecord
	for _, s := range fnSyms {
		if s.Kind == "function" || s.Kind == "method" {
			relSyms = append(relSyms, s)
			if len(relSyms) >= 5 {
				break
			}
		}
	}
	if len(relSyms) > 0 {
		allSymbolsForRel, _ := db.GetAllSymbols(ctx, database)
		symbolsByFile := make(map[string][]db.SymbolRecord)
		for _, s := range allSymbolsForRel {
			symbolsByFile[s.FilePath] = append(symbolsByFile[s.FilePath], s)
		}

		sb.WriteString("## Call Relationships\n")
		for _, s := range relSyms {
			callers, _ := db.FindCallers(ctx, database, s.Name, 6)
			callees, _ := db.FindCallees(ctx, database, s.Name)

			sb.WriteString(fmt.Sprintf("- **%s** (`%s:%d`)\n", s.Name, s.FilePath, s.LineNumber))

			if len(callers) > 0 {
				seen := make(map[string]bool)
				var names []string
				for _, c := range callers {
					label := symbols.EnclosingFunctionName(symbolsByFile[c.FilePath], c.LineNumber)
					if label == "" {
						label = fmt.Sprintf("%s:%d", c.FilePath, c.LineNumber)
					}
					if !seen[label] {
						seen[label] = true
						names = append(names, label)
					}
				}
				sb.WriteString(fmt.Sprintf("  - Called from: %s\n", strings.Join(names, ", ")))
			} else {
				sb.WriteString("  - Called from: (no callers found in indexed code — may be an entry point or unused)\n")
			}

			if len(callees) > 0 {
				names := make([]string, 0, len(callees))
				for i, c := range callees {
					if i >= 8 {
						names = append(names, fmt.Sprintf("+%d more", len(callees)-8))
						break
					}
					names = append(names, c.Name)
				}
				sb.WriteString(fmt.Sprintf("  - Calls: %s\n", strings.Join(names, ", ")))
			}
		}
		sb.WriteString("\n")
	}

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

	var codeFiles, otherFiles []string
	for _, f := range candidateFiles {
		if symbols.IsASTCapableLanguage(scanner.DetectLanguage(f)) {
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
			lang := scanner.DetectLanguage(f)
			skel := symbols.GenerateSkeleton(f, lang, content, nil)
			sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", skel))
		}
	}

	// -------------------------------------------------------------
	// ZONE 3 (RECENCY ANCHOR): Implementation Functions + Unit Tests
	// -------------------------------------------------------------
	if len(fnSyms) > 0 {
		sb.WriteString("## Implementation Functions\n")
		for i, s := range fnSyms {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("... +%d other functions\n", len(fnSyms)-10))
				break
			}
			sb.WriteString(fmt.Sprintf("- `[%s]` **%s** (`%s:%d`)\n  `%s`\n",
				s.Kind, s.Name, s.FilePath, s.LineNumber, s.Signature))
		}
		sb.WriteString("\n")
	}

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
