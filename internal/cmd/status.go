package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/ui"
)

// StatusJSON represents the JSON output format for codive status.
type StatusJSON struct {
	TotalFiles     int            `json:"total_files"`
	TotalSizeBytes int64          `json:"total_size_bytes"`
	TotalSizeHuman string         `json:"total_size_human"`
	LastUpdated    string         `json:"last_updated"`
	Languages      map[string]int `json:"languages"`
}

// RunStatus reads .codive/index.db and prints the repository index status.
func RunStatus(targetDir string, asJSON bool) error {
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
		return fmt.Errorf("failed to open database at %s: %w", dbPath, err)
	}
	defer database.Close()

	ctx := context.Background()
	stats, err := db.GetStats(ctx, database)
	if err != nil {
		return fmt.Errorf("failed to get index stats: %w", err)
	}

	if asJSON {
		lastUpdatedStr := "N/A"
		if !stats.LastUpdated.IsZero() {
			lastUpdatedStr = stats.LastUpdated.UTC().Format(time.RFC3339)
		}
		return ui.PrintJSON(StatusJSON{
			TotalFiles:     stats.TotalFiles,
			TotalSizeBytes: stats.TotalSizeBytes,
			TotalSizeHuman: formatBytes(stats.TotalSizeBytes),
			LastUpdated:    lastUpdatedStr,
			Languages:      stats.LanguageCounts,
		})
	}

	ui.SectionHeader("Index Status")

	ui.KeyValue("Repository", absDir)
	ui.KeyValueAccent("Indexed Files", ui.Count(stats.TotalFiles, "file", "files"))
	ui.KeyValueAccent("Total Size", formatBytes(stats.TotalSizeBytes))
	if stats.LastUpdated.IsZero() {
		ui.KeyValue("Last Updated", "never")
	} else {
		ui.KeyValue("Last Updated", stats.LastUpdated.Local().Format("Mon, 02 Jan 2006 15:04:05 MST"))
	}

	fmt.Println()
	ui.Label("Language Breakdown")

	type langCount struct {
		name  string
		count int
	}
	var sortedLangs []langCount
	for l, c := range stats.LanguageCounts {
		sortedLangs = append(sortedLangs, langCount{name: l, count: c})
	}
	sort.Slice(sortedLangs, func(i, j int) bool {
		if sortedLangs[i].count == sortedLangs[j].count {
			return sortedLangs[i].name < sortedLangs[j].name
		}
		return sortedLangs[i].count > sortedLangs[j].count
	})

	if len(sortedLangs) == 0 {
		ui.ListItem("(no files indexed)", "")
	} else {
		for _, item := range sortedLangs {
			pct := float64(item.count) / float64(stats.TotalFiles) * 100
			bar := renderMiniBar(pct, 16)
			ui.ListItem(
				fmt.Sprintf("%-16s  %s", item.name, bar),
				fmt.Sprintf("%4d files  %5.1f%%", item.count, pct),
			)
		}
	}
	fmt.Println()
	return nil
}

// renderMiniBar renders a compact proportional bar.
func renderMiniBar(pct float64, width int) string {
	filled := int(pct / 100 * float64(width))
	if filled > width {
		filled = width
	}
	bar := ""
	for i := 0; i < filled; i++ {
		bar += "█"
	}
	for i := filled; i < width; i++ {
		bar += "░"
	}
	return ui.Green.Sprint(bar[:filled]) + ui.Dim.Sprint(bar[filled:])
}
