// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/recscse/codive/internal/daemon"
	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/graph"
	"github.com/recscse/codive/internal/indexer"
	"github.com/recscse/codive/internal/logger"
	"github.com/recscse/codive/internal/mcp"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/workspace"
)

// RunServe is what an AI client starts as its MCP server. It forwards the
// client's stdio to the shared codive daemon (starting it if needed), so any
// number of clients share one process and one writer per index. If the daemon
// can't be reached, or CODIVE_STANDALONE=1, it serves on its own instead.
func RunServe(targetDir string, version string) error {
	root := workspace.Resolve(targetDir)
	if os.Getenv("CODIVE_STANDALONE") == "1" {
		return runServeStandalone(root, version)
	}
	first, err := daemon.EnsureAndDial(root, version, spawnDaemon)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codive: the shared daemon is unavailable (%v); serving on its own\n", err)
		return runServeStandalone(root, version)
	}
	used := false
	return daemon.Proxy(os.Stdin, os.Stdout, func() (net.Conn, error) {
		if !used {
			used = true
			return first, nil
		}
		return daemon.EnsureAndDial(root, version, spawnDaemon)
	})
}

// runServeStandalone serves MCP over standard I/O from this process alone,
// with background auto-sync. It is the fallback when the daemon can't run.
func runServeStandalone(targetDir string, version string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}
	if logger.InitLogger(absDir, false) == nil {
		defer logger.Close()
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
	fresh.OnSync = logSync
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// An index built by an older extractor is rebuilt here, off the
		// request path: it can take minutes on a large repository, and tool
		// calls keep being answered from the existing index meanwhile. The
		// check is repeated now and then: when several servers share an
		// index only one of them rebuilds it, and if that one exits midway
		// another has to pick the rebuild up.
		rebuildIfOutdated := func() {
			if rebuilt, err := indexer.RebuildIfOutdated(ctx, database, absDir); err != nil {
				slog.Warn("Re-indexing for the updated symbol extractor failed", "error", err)
			} else if rebuilt {
				slog.Info("Re-indexed for the updated symbol extractor", "path", absDir)
			}
		}
		rebuildIfOutdated()
		// Build the code graph here, off the request path (see RunDaemon).
		if err := graph.EnsureFresh(ctx, database, absDir); err != nil && ctx.Err() == nil {
			slog.Warn("Code graph build failed", "error", err)
		}
		startBackgroundWatcher(ctx, fresh, rebuildIfOutdated)
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
			f.OnSync = logSync
			fresheners[key] = f
		}
		freshMu.Unlock()
		waitForFreshness(ctx, f)
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
	// answered; older than this, the query syncs first. On repositories
	// where a sync pass is slow the allowed age grows with its cost (see
	// queryIndexAge), so queries don't each pay for a full scan.
	queryMaxIndexAge = 2 * time.Second
	// queryAgeFactor scales the allowed index age with the cost of a sync.
	queryAgeFactor = 5
	// queryFreshnessWait is how long a query waits for a sync before it is
	// answered from the index as it is.
	queryFreshnessWait = 5 * time.Second
	// rebuildRecheckInterval is how often a running server checks whether
	// the index still needs rebuilding for a newer symbol extractor.
	rebuildRecheckInterval = 5 * time.Minute
)

// waitForFreshness brings f's index up to date for a tool call. The sync runs
// on its own clock: the call waits a moment for it, then answers from the index
// as it is. A sync that is slow or blocked by another process must not use up
// the call's time budget and turn into a timeout. Its outcome is logged by the
// Freshener's OnSync.
func waitForFreshness(ctx context.Context, f *indexer.Freshener) {
	waitCtx, cancel := context.WithTimeout(ctx, queryFreshnessWait)
	defer cancel()
	_, _ = f.SyncIfOlder(waitCtx, queryIndexAge(f.LastCost()))
}

// queryIndexAge returns how stale the index may be when a query is answered,
// given how long the last sync pass took.
func queryIndexAge(lastCost time.Duration) time.Duration {
	d := lastCost * queryAgeFactor
	if d < queryMaxIndexAge {
		return queryMaxIndexAge
	}
	if d > maxSyncInterval {
		return maxSyncInterval
	}
	return d
}

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

func startBackgroundWatcher(ctx context.Context, fresh *indexer.Freshener, rebuildIfOutdated func()) {
	timer := time.NewTimer(minSyncInterval)
	defer timer.Stop()
	recheck := time.NewTicker(rebuildRecheckInterval)
	defer recheck.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-recheck.C:
			rebuildIfOutdated()
		case <-timer.C:
			// Skipped if a query already synced within the minimum interval.
			// The outcome is logged by the Freshener's OnSync.
			_, _ = fresh.SyncIfOlder(ctx, minSyncInterval)
			timer.Reset(nextSyncDelay(fresh.LastCost()))
		}
	}
}

func logSync(incrResult *scanner.IncrementalResult, err error) { logSyncTo(slog.Default(), incrResult, err) }

func logSyncTo(l *slog.Logger, incrResult *scanner.IncrementalResult, err error) {
	if err != nil {
		l.Warn("Index auto-sync failed", "error", err)
		return
	}
	if incrResult != nil && (len(incrResult.Added) > 0 || len(incrResult.Modified) > 0 || len(incrResult.Deleted) > 0) {
		l.Info("Index auto-sync applied changes",
			"added", len(incrResult.Added),
			"modified", len(incrResult.Modified),
			"deleted", len(incrResult.Deleted))
	}
}
