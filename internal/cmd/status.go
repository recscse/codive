package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/recscse/devctx/internal/db"
	"github.com/recscse/devctx/internal/ui"
)

// StatusJSON represents the JSON output format for ctxd status.
type StatusJSON struct {
	TotalFiles     int            `json:"total_files"`
	TotalSizeBytes int64          `json:"total_size_bytes"`
	TotalSizeHuman string         `json:"total_size_human"`
	LastUpdated    string         `json:"last_updated"`
	Languages      map[string]int `json:"languages"`
}

// RunStatus reads .devctx/index.db and prints the repository indexing status.
func RunStatus(targetDir string, asJSON bool) error {
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

	ui.Header("📊 ctxd Repository Status")
	fmt.Println()
	fmt.Printf("  %s %s\n", ui.Dim.Sprint("Repository:"), ui.Bold.Sprint(absDir))
	fmt.Printf("  %s %s\n", ui.Dim.Sprint("Total Files:"), ui.GreenBold.Sprintf("%d files", stats.TotalFiles))
	fmt.Printf("  %s %s (%d bytes)\n", ui.Dim.Sprint("Total Size: "), ui.CyanBold.Sprint(formatBytes(stats.TotalSizeBytes)), stats.TotalSizeBytes)
	if stats.LastUpdated.IsZero() {
		fmt.Printf("  %s %s\n", ui.Dim.Sprint("Last Updated:"), ui.Yellow.Sprint("N/A"))
	} else {
		fmt.Printf("  %s %s\n", ui.Dim.Sprint("Last Updated:"), stats.LastUpdated.Local().Format(time.RFC1123))
	}

	fmt.Println()
	ui.CyanBold.Println("Language Breakdown:")

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
		ui.Dim.Println("  (no files indexed)")
	} else {
		for _, item := range sortedLangs {
			percentage := float64(item.count) / float64(stats.TotalFiles) * 100
			fmt.Printf("  • %-14s %s (%s)\n",
				ui.Bold.Sprint(item.name),
				ui.Green.Sprintf("%4d files", item.count),
				ui.Dim.Sprintf("%5.1f%%", percentage))
		}
	}

	return nil
}
