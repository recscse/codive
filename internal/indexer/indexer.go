// Package indexer turns scanner results into index updates: it reads changed
// files, extracts their symbols, and writes everything to the database in one
// transaction. It is the single implementation shared by `init`, `update`,
// `watch`, the `serve` background sync, and the MCP server's auto-index.
package indexer

import (
	"context"
	"database/sql"
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
	Files   []db.FileRecord
	Symbols []db.SymbolRecord
	FTS     map[string]string
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
				syms, _ := symbols.ExtractSymbols(f.Path, f.Language, content)
				resultChan <- parseResult{file: f, ok: true, content: string(content), symbols: syms}
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

// Rebuild scans rootDir from scratch and replaces the whole index with the
// result in one transaction, so a failed rebuild leaves the old index intact.
func Rebuild(ctx context.Context, database *sql.DB, rootDir string, onFile ProgressFunc) (*RebuildResult, error) {
	scanRes, err := scanner.Scan(rootDir)
	if err != nil {
		return nil, err
	}
	ext := Extract(rootDir, scanRes.Files, onFile)
	if err := db.ApplyIndexChanges(ctx, database, db.IndexChanges{
		ReplaceAll: true,
		Files:      ext.Files,
		Symbols:    ext.Symbols,
		FTS:        ext.FTS,
	}); err != nil {
		return nil, err
	}
	if err := db.SetMeta(ctx, database, extractorVersionKey, symbols.ExtractorVersion); err != nil {
		return nil, err
	}
	return &RebuildResult{Scan: scanRes, FileCount: len(ext.Files), SymbolCount: len(ext.Symbols)}, nil
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
func RebuildIfOutdated(ctx context.Context, database *sql.DB, rootDir string) (bool, error) {
	if !NeedsReextract(ctx, database) {
		return false, nil
	}
	if _, err := Rebuild(ctx, database, rootDir, nil); err != nil {
		return false, err
	}
	return true, nil
}

// Freshener serializes syncs of one workspace and remembers when the last one
// finished. It lets the MCP server guarantee a maximum index age at the
// moment each query is answered (syncing on demand if needed), so the
// background poll can run infrequently on large repositories without making
// answers stale.
type Freshener struct {
	database *sql.DB
	rootDir  string

	mu       sync.Mutex
	last     time.Time
	lastCost time.Duration
}

// NewFreshener returns a Freshener for the index of rootDir.
func NewFreshener(database *sql.DB, rootDir string) *Freshener {
	return &Freshener{database: database, rootDir: rootDir}
}

// SyncIfOlder syncs the index unless a sync finished less than maxAge ago.
// Callers arriving while a sync is running wait for it and then reuse its
// result instead of starting another. The returned result is nil when no
// sync was needed.
func (f *Freshener) SyncIfOlder(ctx context.Context, maxAge time.Duration) (*scanner.IncrementalResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.last.IsZero() && time.Since(f.last) < maxAge {
		return nil, nil
	}
	start := time.Now()
	res, err := Sync(ctx, f.database, f.rootDir)
	f.lastCost = time.Since(start)
	if err == nil {
		f.last = time.Now()
	}
	return res, err
}

// LastCost reports how long the most recent sync took.
func (f *Freshener) LastCost() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastCost
}
