package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/indexer"
)

// RunWatch starts a continuous watcher that automatically synchronizes index.db on file changes.
func RunWatch(targetDir string, pollInterval time.Duration) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".codive", "index.db")
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
		fmt.Println("\nStopping codive watcher...")
		cancel()
	}()

	if indexer.NeedsReextract(ctx, database) {
		fmt.Println("The symbol extractor changed since this index was built; re-indexing all files...")
		if _, err := indexer.Rebuild(ctx, database, absDir, nil); err != nil {
			return fmt.Errorf("re-index failed: %w", err)
		}
	}

	fmt.Printf("👀 Watching for file changes at %s (polling every %v)...\n", absDir, pollInterval)
	fmt.Println("Press Ctrl+C to stop.")

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			incrResult, err := indexer.Sync(ctx, database, absDir)
			if err != nil || incrResult == nil {
				continue
			}
			if len(incrResult.Added) == 0 && len(incrResult.Modified) == 0 && len(incrResult.Deleted) == 0 {
				continue
			}

			nowStr := time.Now().Format("15:04:05")
			fmt.Printf("[%s] Index synced: +%d added, ~%d modified, -%d deleted\n",
				nowStr, len(incrResult.Added), len(incrResult.Modified), len(incrResult.Deleted))
		}
	}
}
