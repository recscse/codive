// Package indexer turns scanner results into index updates: it reads changed
// files, extracts their symbols, and writes everything to the database in one
// transaction. It is the single implementation shared by `init`, `update`,
// `watch`, the `serve` background sync, and the MCP server's auto-index.
package indexer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/symbols"
)

// ProgressFunc is called once per file as it finishes parsing.
type ProgressFunc func(processed, total int, path string)

// Extracted holds the parse output for a batch of files.
type Extracted struct {
	// Files are the input records that could be read. Unreadable files are
	// left out so they are not marked as indexed and get retried next scan.
	Files       []db.FileRecord
	Symbols     []db.SymbolRecord
	FTS         map[string]string
	SymbolTerms []db.SymbolTerms
}

// ParseFile extracts a file's symbols and their search terms. Every writer of
// the index uses it, so the two never disagree.
func ParseFile(relPath, language string, content []byte) ([]db.SymbolRecord, []db.SymbolTerms) {
	syms, _ := symbols.ExtractSymbols(relPath, language, content)
	return syms, symbols.SearchTerms(language, content, syms)
}

// Extract reads and parses files in parallel.
func Extract(rootDir string, files []db.FileRecord, onFile ProgressFunc) Extracted {
	out := Extracted{
		Files: make([]db.FileRecord, 0, len(files)),
		FTS:   make(map[string]string, len(files)),
	}
	if len(files) == 0 {
		return out
	}

	numWorkers := runtime.NumCPU() * 2
	if numWorkers < 4 {
		numWorkers = 4
	}
	if numWorkers > 32 {
		numWorkers = 32
	}
	if numWorkers > len(files) {
		numWorkers = len(files)
	}

	type parseResult struct {
		file    db.FileRecord
		ok      bool
		content string
		symbols []db.SymbolRecord
		terms   []db.SymbolTerms
	}

	fileChan := make(chan db.FileRecord, len(files))
	resultChan := make(chan parseResult, len(files))

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range fileChan {
				content, err := os.ReadFile(filepath.Join(rootDir, filepath.FromSlash(f.Path)))
				if err != nil {
					resultChan <- parseResult{file: f}
					continue
				}
				syms, terms := ParseFile(f.Path, f.Language, content)
				resultChan <- parseResult{file: f, ok: true, content: string(content), symbols: syms, terms: terms}
			}
		}()
	}
	for _, f := range files {
		fileChan <- f
	}
	close(fileChan)
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	processed := 0
	for res := range resultChan {
		processed++
		if onFile != nil {
			onFile(processed, len(files), res.file.Path)
		}
		if !res.ok {
			continue
		}
		out.Files = append(out.Files, res.file)
		out.FTS[res.file.Path] = res.content
		out.Symbols = append(out.Symbols, res.symbols...)
		out.SymbolTerms = append(out.SymbolTerms, res.terms...)
	}
	return out
}

// Apply writes an incremental scan result into the index.
func Apply(ctx context.Context, database *sql.DB, rootDir string, res *scanner.IncrementalResult) error {
	changed := make([]db.FileRecord, 0, len(res.Added)+len(res.Modified))
	changed = append(changed, res.Added...)
	changed = append(changed, res.Modified...)
	ext := Extract(rootDir, changed, nil)
	return db.ApplyIndexChanges(ctx, database, db.IndexChanges{
		Files:        ext.Files,
		Symbols:      ext.Symbols,
		FTS:          ext.FTS,
		SymbolTerms:  ext.SymbolTerms,
		MetadataOnly: res.MetadataOnly,
		Deleted:      res.Deleted,
	})
}

// HasChanges reports whether an incremental scan result needs writing.
func HasChanges(res *scanner.IncrementalResult) bool {
	return len(res.Added) > 0 || len(res.Modified) > 0 || len(res.Deleted) > 0 || len(res.MetadataOnly) > 0
}

// Sync runs an incremental scan of rootDir against the index and applies
// whatever changed. The returned result is nil when the scan itself failed.
func Sync(ctx context.Context, database *sql.DB, rootDir string) (*scanner.IncrementalResult, error) {
	existing, err := db.GetAllFiles(ctx, database)
	if err != nil {
		return nil, err
	}
	res, err := scanner.ScanIncremental(rootDir, existing)
	if err != nil {
		return nil, err
	}
	if !HasChanges(res) {
		return res, nil
	}
	return res, Apply(ctx, database, rootDir, res)
}

// RebuildResult summarizes a full rebuild.
type RebuildResult struct {
	Scan        *scanner.ScanResult
	FileCount   int
	SymbolCount int
}

// RebuildHooks contains optional callbacks for progress reporting during full rebuild.
type RebuildHooks struct {
	OnScanComplete func(totalFiles int)
	OnFileProgress ProgressFunc
	OnCommitStart  func(totalSymbols, totalFTS int)
	// OnGraphStart is called by callers (not Rebuild itself) just before the
	// code graph build, which has no per-file progress and can take a while.
	OnGraphStart func()
}

// RebuildWithHooks scans rootDir from scratch and replaces the whole index with the
// result in one transaction, notifying hooks along the way.
func RebuildWithHooks(ctx context.Context, database *sql.DB, rootDir string, hooks RebuildHooks) (*RebuildResult, error) {
	scanRes, err := scanner.Scan(rootDir)
	if err != nil {
		return nil, err
	}
	if hooks.OnScanComplete != nil {
		hooks.OnScanComplete(len(scanRes.Files))
	}

	ext := Extract(rootDir, scanRes.Files, hooks.OnFileProgress)

	if hooks.OnCommitStart != nil {
		hooks.OnCommitStart(len(ext.Symbols), len(ext.FTS))
	}

	if err := db.ApplyIndexChanges(ctx, database, db.IndexChanges{
		ReplaceAll:  true,
		Files:       ext.Files,
		Symbols:     ext.Symbols,
		FTS:         ext.FTS,
		SymbolTerms: ext.SymbolTerms,
	}); err != nil {
		return nil, err
	}
	if err := db.SetMeta(ctx, database, extractorVersionKey, symbols.ExtractorVersion); err != nil {
		return nil, err
	}
	return &RebuildResult{Scan: scanRes, FileCount: len(ext.Files), SymbolCount: len(ext.Symbols)}, nil
}

// Rebuild scans rootDir from scratch and replaces the whole index with the
// result in one transaction, so a failed rebuild leaves the old index intact.
func Rebuild(ctx context.Context, database *sql.DB, rootDir string, onFile ProgressFunc) (*RebuildResult, error) {
	return RebuildWithHooks(ctx, database, rootDir, RebuildHooks{OnFileProgress: onFile})
}

const extractorVersionKey = "extractor_version"

// NeedsReextract reports whether the index was built by a different version
// of the symbol extractor, so its symbols are stale even for unchanged files.
func NeedsReextract(ctx context.Context, database *sql.DB) bool {
	v, err := db.GetMeta(ctx, database, extractorVersionKey)
	return err == nil && v != symbols.ExtractorVersion
}

// RebuildIfOutdated rebuilds the index when NeedsReextract says so and
// reports whether it did. A full rebuild can take minutes on a large
// repository, so callers must not run it inside a deadline-bound request:
// the MCP server runs it from its background loop, and queries keep being
// answered from the existing index meanwhile.
//
// Several codive processes often serve the same index (one per AI client),
// and a rebuild holds every file's content in memory while it parses. Only
// the process that takes the rebuild lock rebuilds; the others report false
// and keep answering from the index it is replacing.
func RebuildIfOutdated(ctx context.Context, database *sql.DB, rootDir string) (bool, error) {
	if !NeedsReextract(ctx, database) {
		return false, nil
	}
	release, ok := acquireRebuildLock(rootDir)
	if !ok {
		return false, nil
	}
	defer release()
	// Another process may have finished its rebuild just before the lock was
	// taken.
	if !NeedsReextract(ctx, database) {
		return false, nil
	}
	if _, err := Rebuild(ctx, database, rootDir, nil); err != nil {
		return false, err
	}
	return true, nil
}

const (
	// rebuildLockStale is how long a rebuild lock may go without being
	// refreshed before it is taken to be left over from a process that died.
	rebuildLockStale = 10 * time.Minute
	// rebuildLockRefresh is how often the process holding the lock refreshes it.
	rebuildLockRefresh = time.Minute
)

// acquireRebuildLock takes the cross-process lock for rebuilding rootDir's
// index: a file in .codive that exists while a rebuild runs. ok is false when
// another live process holds it. If the lock file can't be created at all
// (a read-only directory), the rebuild goes ahead unlocked instead of never
// happening.
func acquireRebuildLock(rootDir string) (release func(), ok bool) {
	path := filepath.Join(rootDir, ".codive", "rebuild.lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			fmt.Fprintln(f, os.Getpid())
			f.Close()
			stop := make(chan struct{})
			go func() {
				t := time.NewTicker(rebuildLockRefresh)
				defer t.Stop()
				for {
					select {
					case <-stop:
						return
					case now := <-t.C:
						_ = os.Chtimes(path, now, now)
					}
				}
			}()
			return func() {
				close(stop)
				_ = os.Remove(path)
			}, true
		}
		if !os.IsExist(err) {
			return func() {}, true
		}
		info, statErr := os.Stat(path)
		if statErr == nil && time.Since(info.ModTime()) < rebuildLockStale {
			return nil, false
		}
		// Stale, or released since the attempt above: clear it and try once more.
		_ = os.Remove(path)
	}
	return nil, false
}

// ErrSyncPending is returned by SyncIfOlder when the caller's context ended
// before the sync it was waiting for finished. The sync keeps running.
var ErrSyncPending = errors.New("index sync is still running")

const (
	// maxSyncDuration bounds a single sync pass.
	maxSyncDuration = 15 * time.Minute
	// After a failed sync (typically another process holding the database's
	// write lock) the next attempt waits, doubling up to the maximum, so a
	// stuck index isn't retried by every single query.
	minSyncRetryDelay = 2 * time.Second
	maxSyncRetryDelay = time.Minute
)

// Freshener runs the syncs of one workspace, one at a time, and remembers
// when the last one finished. It lets the MCP server bound how stale the
// index is when a query is answered (syncing on demand if needed), so the
// background poll can run infrequently on large repositories.
//
// A sync runs on its own clock, not its caller's: SyncIfOlder waits for it
// only as long as the caller's context allows. A query that needs a fresh
// index therefore waits briefly and is otherwise answered from the index as
// it is, instead of spending its whole time budget on a sync that is slow or
// blocked and then failing.
type Freshener struct {
	database *sql.DB
	rootDir  string

	// OnSync, if set, is called with the outcome of every sync pass that
	// runs. Set it before the first SyncIfOlder call.
	OnSync func(*scanner.IncrementalResult, error)

	mu       sync.Mutex
	last     time.Time
	lastCost time.Duration
	running  *syncRun // the sync in flight, nil when idle
	failures int      // consecutive failed syncs
	retryAt  time.Time
}

type syncRun struct {
	done chan struct{} // closed when the sync ends; res and err are set before
	res  *scanner.IncrementalResult
	err  error
}

// NewFreshener returns a Freshener for the index of rootDir.
func NewFreshener(database *sql.DB, rootDir string) *Freshener {
	return &Freshener{database: database, rootDir: rootDir}
}

// SyncIfOlder syncs the index unless a sync finished less than maxAge ago,
// and waits for the result until ctx ends. The returned result is nil when
// no sync was needed, when the caller joined a sync another caller started,
// or when the last sync failed too recently to try again yet. If ctx ends
// first, the error wraps ErrSyncPending and the sync carries on; its outcome
// still reaches OnSync.
func (f *Freshener) SyncIfOlder(ctx context.Context, maxAge time.Duration) (*scanner.IncrementalResult, error) {
	f.mu.Lock()
	if !f.last.IsZero() && time.Since(f.last) < maxAge {
		f.mu.Unlock()
		return nil, nil
	}
	run, started := f.running, false
	if run == nil {
		if time.Now().Before(f.retryAt) {
			f.mu.Unlock()
			return nil, nil
		}
		run, started = &syncRun{done: make(chan struct{})}, true
		f.running = run
		go f.run(run)
	}
	f.mu.Unlock()

	select {
	case <-run.done:
		if started {
			return run.res, run.err
		}
		return nil, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %v", ErrSyncPending, ctx.Err())
	}
}

func (f *Freshener) run(r *syncRun) {
	ctx, cancel := context.WithTimeout(context.Background(), maxSyncDuration)
	defer cancel()
	start := time.Now()
	res, err := Sync(ctx, f.database, f.rootDir)

	f.mu.Lock()
	f.lastCost = time.Since(start)
	if err == nil {
		f.last = time.Now()
		f.failures = 0
		f.retryAt = time.Time{}
	} else {
		f.failures++
		f.retryAt = time.Now().Add(syncRetryDelay(f.failures))
	}
	f.running = nil
	onSync := f.OnSync
	f.mu.Unlock()

	r.res, r.err = res, err
	if onSync != nil {
		onSync(res, err)
	}
	close(r.done)
}

// syncRetryDelay returns how long to wait before syncing again after the
// given number of consecutive failures.
func syncRetryDelay(failures int) time.Duration {
	d := minSyncRetryDelay
	for i := 1; i < failures && d < maxSyncRetryDelay; i++ {
		d *= 2
	}
	if d > maxSyncRetryDelay {
		d = maxSyncRetryDelay
	}
	return d
}

// LastCost reports how long the most recent sync took.
func (f *Freshener) LastCost() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastCost
}
