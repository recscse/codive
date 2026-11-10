package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/indexer"
	"github.com/recscse/codive/internal/mcp"
	"github.com/recscse/codive/internal/ui"
)

// RunBench measures read_symbol intent search on targetDir's own history:
// each recent commit's subject is a query and the definitions it changed
// are the answers. See mcp.RunIntentBenchmark.
func RunBench(targetDir string, maxCommits, show int, asJSON bool) error {
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
	if _, err := indexer.RebuildIfOutdated(ctx, database, absDir); err != nil {
		return err
	}
	if _, err := indexer.Sync(ctx, database, absDir); err != nil {
		return err
	}

	rep, err := mcp.RunIntentBenchmark(ctx, database, absDir, maxCommits)
	if err != nil {
		return err
	}
	if asJSON {
		return ui.PrintJSON(rep)
	}

	ui.SectionHeader("Intent search benchmark (questions from git history)")
	ui.KeyValue("Commits scanned", fmt.Sprint(rep.CommitsScanned))
	ui.KeyValue("Used as questions", fmt.Sprint(len(rep.Cases)))
	for reason, n := range rep.Skipped {
		ui.KeyValue("  skipped", fmt.Sprintf("%d: %s", n, reason))
	}
	if len(rep.Cases) == 0 {
		fmt.Println()
		return nil
	}
	model := "starting weights"
	if rep.ModelVersion > 0 {
		model = fmt.Sprintf("learned model v%d", rep.ModelVersion)
	}
	ui.KeyValue("Ranker", model)
	fmt.Println()
	pct := func(f float64) string { return fmt.Sprintf("%5.1f%%", 100*f) }
	row := func(name string, s mcp.RankStats) {
		ui.KeyValue(name, fmt.Sprintf("top1 %s   top3 %s   top10 %s   MRR %.2f", pct(s.Hit1), pct(s.Hit3), pct(s.Hit10), s.MRR))
	}
	row("Right function", rep.Symbol)
	row("Right file (intent)", rep.File)
	row("Right file (keyword)", rep.KeywordFile)
	ui.KeyValue("Median latency", rep.MedianLatency.String())
	fmt.Println()
	fmt.Println("  Keyword = files ranked by how often the query's words occur, like grepping for them.")
	fmt.Println("  Commits can add the words of their own subject to the code, which flatters both rows.")

	if show > 0 {
		ui.SubHeader(fmt.Sprintf("First %d misses (right function not in top 3)", show))
		n := 0
		for _, c := range rep.Cases {
			if c.SymbolRank >= 1 && c.SymbolRank <= 3 {
				continue
			}
			fmt.Printf("  %s %q\n      want %s\n      got  %s\n", c.Commit, c.Query, strings.Join(c.Answers, ", "), strings.Join(c.Top, ", "))
			if n++; n == show {
				break
			}
		}
	}
	fmt.Println()
	return nil
}
