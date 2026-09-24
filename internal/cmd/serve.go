// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/indexer"
	"github.com/recscse/codive/internal/mcp"
)

// RunServe starts the MCP (Model Context Protocol) JSON-RPC server over standard I/O with background auto-sync.
func RunServe(targetDir string, version string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".codive", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// Auto-initialize silently: stdout here IS the JSON-RPC transport, so
		// RunInit's progress bar and summary banner (both written to stdout)
		// would corrupt it before the first real protocol message ever goes out.
		_ = RunInitSilent(absDir)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	// Start background live auto-sync worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go startBackgroundWatcher(ctx, absDir, database)

	server := mcp.NewServer(absDir, database, version)
	defer server.Close()
	return server.Serve(os.Stdin, os.Stdout)
}

func startBackgroundWatcher(ctx context.Context, rootDir string, database *sql.DB) {
	ticker := time.NewTicker(2500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncQuietly(ctx, rootDir, database)
		}
	}
}

func syncQuietly(ctx context.Context, rootDir string, database *sql.DB) {
	incrResult, err := indexer.Sync(ctx, database, rootDir)
	if err != nil {
		slog.Warn("Auto-sync background worker failed", "error", err)
		return
	}
	if len(incrResult.Added) > 0 || len(incrResult.Modified) > 0 || len(incrResult.Deleted) > 0 {
		slog.Info("Auto-sync background worker detected changes",
			"added", len(incrResult.Added),
			"modified", len(incrResult.Modified),
			"deleted", len(incrResult.Deleted))
	}
}
