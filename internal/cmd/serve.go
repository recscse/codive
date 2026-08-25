// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/recscse/ctxd/internal/db"
	"github.com/recscse/ctxd/internal/mcp"
	"github.com/recscse/ctxd/internal/scanner"
	"github.com/recscse/ctxd/internal/symbols"
)

// RunServe starts the MCP (Model Context Protocol) JSON-RPC server over standard I/O with background auto-sync.
func RunServe(targetDir string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".ctxd", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// Auto-initialize if not initialized yet
		_ = RunInit(absDir)
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

	server := mcp.NewServer(absDir, database)
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
	existingMap, err := db.GetAllFiles(ctx, database)
	if err != nil {
		return
	}

	incrResult, err := scanner.ScanIncremental(rootDir, existingMap)
	if err != nil {
		return
	}

	if len(incrResult.Added) == 0 && len(incrResult.Modified) == 0 && len(incrResult.Deleted) == 0 {
		return
	}

	slog.Info("Auto-sync background worker detected changes",
		"added", len(incrResult.Added),
		"modified", len(incrResult.Modified),
		"deleted", len(incrResult.Deleted))

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
			fullPath := filepath.Join(rootDir, filepath.FromSlash(f.Path))
			content, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			syms, err := symbols.ExtractSymbols(f.Path, f.Language, content)
			if err == nil && len(syms) > 0 {
				newSymbols = append(newSymbols, syms...)
			}
			ftsFiles[f.Path] = string(content)
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
}
