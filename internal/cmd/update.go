package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/recscse/devctx/internal/db"
	"github.com/recscse/devctx/internal/scanner"
	"github.com/recscse/devctx/internal/symbols"
	"github.com/recscse/devctx/internal/ui"
)

// RunUpdate performs incremental scanning and synchronizes .devctx/index.db with the repo.
func RunUpdate(targetDir string) error {
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

	// 1. Save added and modified files
	toSave := append(incrResult.Added, incrResult.Modified...)
	if len(toSave) > 0 {
		if err := db.SaveFiles(ctx, database, toSave); err != nil {
			return fmt.Errorf("failed to update changed files: %w", err)
		}

		// Delete old symbols for modified files before re-extracting
		var changedPaths []string
		for _, f := range toSave {
			changedPaths = append(changedPaths, f.Path)
		}
		if err := db.DeleteSymbolsForFiles(ctx, database, changedPaths); err != nil {
			return fmt.Errorf("failed to clear old symbols: %w", err)
		}

		// Extract and save new symbols & FTS content
		var newSymbols []db.SymbolRecord
		ftsFiles := make(map[string]string)
		for _, f := range toSave {
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
			newSymbols = append(newSymbols, syms...)
		}
		if len(newSymbols) > 0 {
			if err := db.SaveSymbols(ctx, database, newSymbols); err != nil {
				return fmt.Errorf("failed to save symbols: %w", err)
			}
		}
		if len(ftsFiles) > 0 {
			if err := db.SaveFTS(ctx, database, ftsFiles); err != nil {
				return fmt.Errorf("failed to save fts index: %w", err)
			}
		}
	}

	// 2. Remove deleted files (also removes symbols and FTS records)
	if len(incrResult.Deleted) > 0 {
		if err := db.DeleteFiles(ctx, database, incrResult.Deleted); err != nil {
			return fmt.Errorf("failed to remove deleted files: %w", err)
		}
		if err := db.DeleteFTSForFiles(ctx, database, incrResult.Deleted); err != nil {
			return fmt.Errorf("failed to remove deleted fts records: %w", err)
		}
	}

	duration := time.Since(startTime)

	fmt.Println()
	ui.Divider()
	ui.Success("🔄 ctxd update complete!")
	fmt.Printf("  %s  %s, %s, %s, %s\n",
		ui.Dim.Sprint("Changes:      "),
		ui.Green.Sprintf("+%d added", len(incrResult.Added)),
		ui.Yellow.Sprintf("~%d modified", len(incrResult.Modified)),
		ui.Red.Sprintf("-%d deleted", len(incrResult.Deleted)),
		ui.Dim.Sprintf("%d unchanged", incrResult.UnchangedCount))
	fmt.Printf("  %s  %s (%s)\n",
		ui.Dim.Sprint("Total Indexed:"),
		ui.GreenBold.Sprintf("%d files", len(existing)+len(incrResult.Added)-len(incrResult.Deleted)),
		formatBytes(incrResult.TotalSizeBytes))
	fmt.Printf("  %s  %s\n", ui.Dim.Sprint("Time Elapsed: "), duration.Round(time.Millisecond))
	ui.Divider()

	return nil
}
