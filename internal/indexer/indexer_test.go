package indexer

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/recscse/codive/internal/db"
)

func TestRebuildAndSync(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
	write("a.go", "package p\n\nfunc Alpha() {}\n")
	write("b.go", "package p\n\nfunc Beta() {}\n")

	database, err := db.Open(filepath.Join(dir, ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()
	ctx := context.Background()

	res, err := Rebuild(ctx, database, dir, nil)
	if err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	if res.FileCount != 2 || res.SymbolCount != 2 {
		t.Fatalf("expected 2 files / 2 symbols, got %d / %d", res.FileCount, res.SymbolCount)
	}

	// Modify (and move a symbol), delete, and add.
	write("a.go", "package p\n\n\n\nfunc Alpha() {}\n")
	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatalf("failed to remove b.go: %v", err)
	}
	write("c.go", "package p\n\nfunc Gamma() {}\n")

	incr, err := Sync(ctx, database, dir)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(incr.Added) != 1 || len(incr.Modified) != 1 || len(incr.Deleted) != 1 {
		t.Fatalf("unexpected change set: added=%d modified=%d deleted=%v", len(incr.Added), len(incr.Modified), incr.Deleted)
	}

	if syms, _ := db.FindSymbolsInFile(ctx, database, "a.go"); len(syms) != 1 || syms[0].LineNumber != 5 {
		t.Errorf("expected Alpha only at L5, got %+v", syms)
	}
	if syms, _ := db.FindSymbols(ctx, database, "Beta"); len(syms) != 0 {
		t.Errorf("deleted file's symbol survived: %+v", syms)
	}
	if hits, _ := db.SearchFTS(ctx, database, "Beta", 5); len(hits) != 0 {
		t.Errorf("deleted file's content survived in FTS: %+v", hits)
	}
	if syms, _ := db.FindSymbols(ctx, database, "Gamma"); len(syms) != 1 {
		t.Errorf("added file's symbol missing: %+v", syms)
	}
	if hits, _ := db.SearchFTS(ctx, database, "Alpha", 5); len(hits) != 1 {
		t.Errorf("expected exactly one FTS row for a.go, got %+v", hits)
	}

	// A second sync with nothing changed is a no-op.
	incr, err = Sync(ctx, database, dir)
	if err != nil || HasChanges(incr) {
		t.Errorf("expected no changes on second sync, got err=%v changes=%+v", err, incr)
	}
}

func TestFreshenerSyncIfOlder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n\nfunc A() {}\n"), 0644); err != nil {
		t.Fatalf("failed to write a.go: %v", err)
	}
	database, err := db.Open(filepath.Join(dir, ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := Rebuild(ctx, database, dir, nil); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}

	f := NewFreshener(database, dir)
	if res, err := f.SyncIfOlder(ctx, time.Hour); err != nil || res == nil {
		t.Fatalf("first call must sync (nothing recorded yet): res=%v err=%v", res, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package p\n\nfunc B() {}\n"), 0644); err != nil {
		t.Fatalf("failed to write b.go: %v", err)
	}
	if res, _ := f.SyncIfOlder(ctx, time.Hour); res != nil {
		t.Errorf("sync within maxAge should be skipped, got %+v", res)
	}
	res, err := f.SyncIfOlder(ctx, 0)
	if err != nil || res == nil || len(res.Added) != 1 {
		t.Fatalf("stale index must sync and pick up b.go: res=%+v err=%v", res, err)
	}
	if syms, _ := db.FindSymbols(ctx, database, "B"); len(syms) == 0 {
		t.Error("B not indexed after on-demand sync")
	}

	// Concurrent callers share one sync instead of racing.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.SyncIfOlder(ctx, time.Hour); err != nil {
				t.Errorf("concurrent sync failed: %v", err)
			}
		}()
	}
	wg.Wait()
}

// An index built by a different extractor version is rebuilt once, so fixes
// to symbol extraction reach files that haven't changed since.
func TestRebuildIfOutdated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n\ntype S struct{}\n\nfunc (s *S) M() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(dir, ".codive", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()

	if !NeedsReextract(ctx, database) {
		t.Error("a fresh index with no recorded extractor version should need re-extraction")
	}
	if _, err := Rebuild(ctx, database, dir, nil); err != nil {
		t.Fatal(err)
	}
	if NeedsReextract(ctx, database) {
		t.Error("Rebuild should record the current extractor version")
	}
	if rebuilt, err := RebuildIfOutdated(ctx, database, dir); err != nil || rebuilt {
		t.Errorf("up-to-date index was rebuilt: %v %v", rebuilt, err)
	}

	// Simulate an index from an older extractor with a stale signature.
	if err := db.SetMeta(ctx, database, extractorVersionKey, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE symbols SET signature = 'func  M()' WHERE name = 'M';"); err != nil {
		t.Fatal(err)
	}
	if rebuilt, err := RebuildIfOutdated(ctx, database, dir); err != nil || !rebuilt {
		t.Fatalf("outdated index was not rebuilt: %v %v", rebuilt, err)
	}
	syms, _ := db.FindSymbolsByName(ctx, database, "M")
	if len(syms) != 1 || syms[0].Signature != "func (s *S) M()" {
		t.Errorf("stale signature survived the rebuild: %+v", syms)
	}
}
