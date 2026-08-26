package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/recscse/devctx/internal/db"
	"github.com/recscse/devctx/internal/ui"
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

	dbPath := filepath.Join(absDir, ".devctx", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("repository is not initialized (no index found at %s). Run 'devctx init' first", dbPath)
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
		ui.Warning(fmt.Sprintf("No matches found for '%s'", query))
		return nil
	}

	fmt.Println()
	ui.Header(fmt.Sprintf("devctx — Search Results: '%s' (%d matches)", query, len(results)))
	ui.Divider()
	for i, res := range results {
		fmt.Printf("  %s %s\n", ui.Dim.Sprintf("%2d.", i+1), ui.GreenBold.Sprint(res.Path))
		cleanSnippet := strings.ReplaceAll(res.Snippet, ">>>", "")
		cleanSnippet = strings.ReplaceAll(cleanSnippet, "<<<", "")
		cleanSnippet = strings.TrimSpace(cleanSnippet)
		lines := strings.Split(cleanSnippet, "\n")
		for _, l := range lines {
			fmt.Printf("      %s %s\n", ui.Dim.Sprint("│"), strings.TrimRight(l, "\r"))
		}
		fmt.Println()
	}
	ui.Divider()
	fmt.Println()

	return nil
}
