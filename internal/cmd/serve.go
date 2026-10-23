// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/indexer"
	"github.com/recscse/codive/internal/mcp"
	"github.com/recscse/codive/internal/scanner"
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

	// Keep the index in sync two ways: a background poll whose interval adapts
	// to repository size, plus an on-demand sync before each index query when
	// the last sync is older than queryMaxIndexAge. Together they bound how
	// stale any answer can be without polling large repos continuously.
	fresh := indexer.NewFreshener(database, absDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// An index built by an older extractor is rebuilt here, off the
		// request path: it can take minutes on a large repository, and tool
		// calls keep being answered from the existing index meanwhile.
		if rebuilt, err := indexer.RebuildIfOutdated(ctx, database, absDir); err != nil {
			slog.Warn("Re-indexing for the updated symbol extractor failed", "error", err)
		} else if rebuilt {
			slog.Info("Re-indexed for the updated symbol extractor", "path", absDir)
		}
		startBackgroundWatcher(ctx, fresh)
	}()

	server := mcp.NewServer(absDir, database, version)
	defer server.Close()
	// One Freshener per workspace: the one started with, plus any the client
	// switches to (MCP roots) or a tool call targets via workspace_path.
	var freshMu sync.Mutex
	fresheners := map[string]*indexer.Freshener{filepath.Clean(absDir): fresh}
	server.SetFreshnessHook(func(ctx context.Context, dir string, db *sql.DB) {
		key := filepath.Clean(dir)
		freshMu.Lock()
		f, ok := fresheners[key]
		if !ok {
			f = indexer.NewFreshener(db, dir)
			fresheners[key] = f
		}
		freshMu.Unlock()
		logSync(f.SyncIfOlder(ctx, queryMaxIndexAge))
	})
	return server.Serve(os.Stdin, os.Stdout)
}

const (
	// minSyncInterval is the background sync period for small repositories.
	minSyncInterval = 2500 * time.Millisecond
	// maxSyncInterval bounds how stale the index can get on huge ones.
	maxSyncInterval = 30 * time.Second
	// syncIdleFactor keeps background syncing to roughly 1/(factor+1) of one
	// core at most: after a pass taking d, the next starts no sooner than
	// factor*d later. Files an agent actually queries don't depend on this
	// interval: find_symbol, get_file_skeleton, and read_file_context re-check
	// the files they touch on every call.
	syncIdleFactor = 20
	// queryMaxIndexAge is the most stale the index may be when a query is
	// answered; older than this, the query syncs first.
	queryMaxIndexAge = 2 * time.Second
)

// nextSyncDelay returns how long to wait after a sync pass that took elapsed.
func nextSyncDelay(elapsed time.Duration) time.Duration {
	d := elapsed * syncIdleFactor
	if d < minSyncInterval {
		return minSyncInterval
	}
	if d > maxSyncInterval {
		return maxSyncInterval
	}
	return d
}

func startBackgroundWatcher(ctx context.Context, fresh *indexer.Freshener) {
	timer := time.NewTimer(minSyncInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			// Skipped if a query already synced within the minimum interval.
			logSync(fresh.SyncIfOlder(ctx, minSyncInterval))
			timer.Reset(nextSyncDelay(fresh.LastCost()))
		}
	}
}

func logSync(incrResult *scanner.IncrementalResult, err error) {
	if err != nil {
		slog.Warn("Index auto-sync failed", "error", err)
		return
	}
	if incrResult != nil && (len(incrResult.Added) > 0 || len(incrResult.Modified) > 0 || len(incrResult.Deleted) > 0) {
		slog.Info("Index auto-sync applied changes",
			"added", len(incrResult.Added),
			"modified", len(incrResult.Modified),
			"deleted", len(incrResult.Deleted))
	}
}
