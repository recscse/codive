package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/indexer"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/ui"
)

// RunUpdate performs incremental scanning and synchronizes .codive/index.db with the repo.
func RunUpdate(targetDir string) error {
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
	if indexer.NeedsReextract(ctx, database) {
		ui.Info("The symbol extractor changed since this index was built; re-indexing all files...")
		start := time.Now()
		res, err := indexer.Rebuild(ctx, database, absDir, nil)
		if err != nil {
			return fmt.Errorf("re-index failed: %w", err)
		}
		ui.Success(fmt.Sprintf("Re-indexed %s (%s symbols) in %v.", ui.Count(res.FileCount, "file", "files"),
			fmt.Sprint(res.SymbolCount), time.Since(start).Round(time.Millisecond)))
		return nil
	}

	existing, err := db.GetAllFiles(ctx, database)
	if err != nil {
		return fmt.Errorf("failed to load existing index: %w", err)
	}

	slog.Info("Starting incremental synchronization", "path", absDir)
	startTime := time.Now()

	incrResult, err := scanner.ScanIncremental(absDir, existing)
	if err != nil {
		slog.Error("Incremental scan failed", "error", err)
		return fmt.Errorf("incremental scan failed: %w", err)
	}

	slog.Info("Detected file changes",
		"added", len(incrResult.Added),
		"modified", len(incrResult.Modified),
		"deleted", len(incrResult.Deleted),
		"unchanged", incrResult.UnchangedCount)

	if indexer.HasChanges(incrResult) {
		if err := indexer.Apply(ctx, database, absDir, incrResult); err != nil {
			return fmt.Errorf("failed to update index: %w", err)
		}
	}

	duration := time.Since(startTime)

	fmt.Println()
	ui.Divider()
	ui.Success("codive update complete!")
	fmt.Printf("  %s  %s, %s, %s, %s\n",
		ui.Dim.Sprint("Changes:      "),
		ui.Green.Sprintf("+%d added", len(incrResult.Added)),
		ui.Yellow.Sprintf("~%d modified", len(incrResult.Modified)),
		ui.Red.Sprintf("-%d deleted", len(incrResult.Deleted)),
		ui.Dim.Sprintf("%d unchanged", incrResult.UnchangedCount))
	fmt.Printf("  %s  %s (%s)\n",
		ui.Dim.Sprint("Total Indexed:"),
		ui.GreenBold.Sprint(ui.Count(len(existing)+len(incrResult.Added)-len(incrResult.Deleted), "file", "files")),
		formatBytes(incrResult.TotalSizeBytes))
	fmt.Printf("  %s  %s\n", ui.Dim.Sprint("Time Elapsed: "), duration.Round(time.Millisecond))
	ui.Divider()

	return nil
}
