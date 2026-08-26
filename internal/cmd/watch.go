package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/recscse/devctx/internal/db"
	"github.com/recscse/devctx/internal/scanner"
	"github.com/recscse/devctx/internal/symbols"
)

// RunWatch starts a continuous watcher that automatically synchronizes index.db on file changes.
func RunWatch(targetDir string, pollInterval time.Duration) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".devctx", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Println("Index not initialized. Running initial scan...")
		if err := RunInit(absDir); err != nil {
			return err
		}
	}

	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nStopping ctxd watcher...")
		cancel()
	}()

	fmt.Printf("👀 Watching for file changes at %s (polling every %v)...\n", absDir, pollInterval)
	fmt.Println("Press Ctrl+C to stop.")

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			existing, err := db.GetAllFiles(ctx, database)
			if err != nil {
				continue
			}

			incrResult, err := scanner.ScanIncremental(absDir, existing)
			if err != nil {
				continue
			}

			hasChanges := len(incrResult.Added) > 0 || len(incrResult.Modified) > 0 || len(incrResult.Deleted) > 0
			if !hasChanges {
				continue
			}

			// Apply changes
			toSave := append(incrResult.Added, incrResult.Modified...)
			if len(toSave) > 0 {
				_ = db.SaveFiles(ctx, database, toSave)

				var changedPaths []string
				for _, f := range toSave {
					changedPaths = append(changedPaths, f.Path)
				}
				_ = db.DeleteSymbolsForFiles(ctx, database, changedPaths)

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
					if err == nil {
						newSymbols = append(newSymbols, syms...)
					}
				}
				if len(newSymbols) > 0 {
					_ = db.SaveSymbols(ctx, database, newSymbols)
				}
				if len(ftsFiles) > 0 {
					_ = db.SaveFTS(ctx, database, ftsFiles)
				}
			}

			if len(incrResult.Deleted) > 0 {
				_ = db.DeleteFiles(ctx, database, incrResult.Deleted)
				_ = db.DeleteFTSForFiles(ctx, database, incrResult.Deleted)
			}

			nowStr := time.Now().Format("15:04:05")
			fmt.Printf("[%s] Index synced: +%d added, ~%d modified, -%d deleted\n",
				nowStr, len(incrResult.Added), len(incrResult.Modified), len(incrResult.Deleted))
		}
	}
}
