// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/recscse/devctx/internal/db"
	"github.com/recscse/devctx/internal/scanner"
	"github.com/recscse/devctx/internal/symbols"
	"github.com/recscse/devctx/internal/ui"
)

// formatBytes converts byte count to human-readable format.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// RunInit initializes the repository index by scanning files and populating .devctx/index.db.
func RunInit(targetDir string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	stat, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("cannot access directory %s: %w", absDir, err)
	}
	if !stat.IsDir() {
		return fmt.Errorf("path %s is not a directory", absDir)
	}

	slog.Info("Starting repository initialization", "path", absDir)
	startTime := time.Now()

	scanResult, err := scanner.Scan(absDir)
	if err != nil {
		slog.Error("Failed to scan repository", "error", err)
		return fmt.Errorf("scan failed: %w", err)
	}

	ctxdDir := filepath.Join(absDir, ".devctx")
	dbPath := filepath.Join(ctxdDir, "index.db")

	database, err := db.Open(dbPath)
	if err != nil {
		slog.Error("Failed to open database", "db_path", dbPath, "error", err)
		return fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	if err := db.InitSchema(database); err != nil {
		return fmt.Errorf("failed to initialize schema: %w", err)
	}

	ctx := context.Background()
	if err := db.SaveFiles(ctx, database, scanResult.Files); err != nil {
		return fmt.Errorf("failed to save file records: %w", err)
	}

	// Extract and save symbols & full-text content
	var allSymbols []db.SymbolRecord
	ftsFiles := make(map[string]string)
	for _, f := range scanResult.Files {
		fullPath := filepath.Join(absDir, filepath.FromSlash(f.Path))
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		ftsFiles[f.Path] = string(content)
		syms, err := symbols.ExtractSymbols(f.Path, f.Language, content)
		if err != nil {
			continue
		}
		allSymbols = append(allSymbols, syms...)
	}

	if len(allSymbols) > 0 {
		if err := db.SaveSymbols(ctx, database, allSymbols); err != nil {
			return fmt.Errorf("failed to save symbols: %w", err)
		}
	}

	if len(ftsFiles) > 0 {
		if err := db.SaveFTS(ctx, database, ftsFiles); err != nil {
			return fmt.Errorf("failed to save full-text search index: %w", err)
		}
	}

	duration := time.Since(startTime)

	// Determine primary language(s)
	type langCount struct {
		name  string
		count int
	}
	var sortedLangs []langCount
	for l, c := range scanResult.LanguageCounts {
		sortedLangs = append(sortedLangs, langCount{name: l, count: c})
	}
	sort.Slice(sortedLangs, func(i, j int) bool {
		if sortedLangs[i].count == sortedLangs[j].count {
			return sortedLangs[i].name < sortedLangs[j].name
		}
		return sortedLangs[i].count > sortedLangs[j].count
	})

	primaryLang := "None"
	if len(sortedLangs) > 0 {
		primaryLang = sortedLangs[0].name
	}

	fmt.Println()
	ui.Divider()
	ui.Success("✨ ctxd initialization complete!")
	fmt.Printf("  %s  %s (%s)\n", ui.Dim.Sprint("Indexed:          "), ui.GreenBold.Sprintf("%d files", len(scanResult.Files)), formatBytes(scanResult.TotalSizeBytes))
	fmt.Printf("  %s  %s\n", ui.Dim.Sprint("Symbols Extracted:"), ui.CyanBold.Sprintf("%d symbols", len(allSymbols)))
	fmt.Printf("  %s  %s\n", ui.Dim.Sprint("Primary Language: "), ui.Bold.Sprint(primaryLang))
	fmt.Printf("  %s  %s\n", ui.Dim.Sprint("Database:         "), dbPath)
	fmt.Printf("  %s  %s\n", ui.Dim.Sprint("Time Elapsed:     "), duration.Round(time.Millisecond))
	ui.Divider()

	return nil
}
