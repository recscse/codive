package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/ui"
)

// RunSearch executes full-text search across indexed files.
func RunSearch(targetDir string, query string, limit int, asJSON bool) error {
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("search query cannot be empty")
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".codive", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("repository not initialized — no index at %s\n  Run 'codive init' first", dbPath)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	ctx := context.Background()
	results, err := db.SearchFTS(ctx, database, query, limit)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if asJSON {
		return ui.PrintJSON(results)
	}

	if len(results) == 0 {
		ui.Warning(fmt.Sprintf("No results for '%s'", query))
		return nil
	}

	ui.SectionHeader(fmt.Sprintf("Search Results  (%s for '%s')", ui.Count(len(results), "match", "matches"), query))

	for i, res := range results {
		fmt.Println()
		fmt.Printf("  %s  %s\n",
			ui.Dim.Sprintf("%2d.", i+1),
			ui.GreenBold.Sprint(res.Path),
		)
		// Clean FTS5 snippet markers
		snippet := strings.ReplaceAll(res.Snippet, ">>>", "")
		snippet = strings.ReplaceAll(snippet, "<<<", "")
		snippet = strings.TrimSpace(snippet)
		for _, line := range strings.Split(snippet, "\n") {
			trimmed := strings.TrimRight(line, "\r ")
			if trimmed == "" {
				continue
			}
			fmt.Printf("     %s  %s\n", ui.Dim.Sprint("│"), ui.Dim.Sprint(trimmed))
		}
	}
	fmt.Println()
	ui.Divider()
	fmt.Println()
	return nil
}
