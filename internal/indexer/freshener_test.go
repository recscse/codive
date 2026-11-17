package indexer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/scanner"
)

func newIndexedDir(t *testing.T) (string, *Freshener, func()) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n\nfunc A() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(dir, ".codive", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	closeDB := func() {
		if !closed {
			closed = true
			database.Close()
		}
	}
	t.Cleanup(closeDB)
	if _, err := Rebuild(context.Background(), database, dir, nil); err != nil {
		t.Fatal(err)
	}
	return dir, NewFreshener(database, dir), closeDB
}

// A caller that stops waiting must not cancel the sync: a tool call gives a
// sync a few seconds, and the work done so far would otherwise be rolled back
// and repeated by the next call, never finishing on a large repository.
func TestSyncOutlivesACallerThatStopsWaiting(t *testing.T) {
	dir, f, _ := newIndexedDir(t)
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package p\n\nfunc B() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	f.OnSync = func(_ *scanner.IncrementalResult, err error) { done <- err }

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	// Either the sync won the race and its result came back, or the caller
	// left first; both are fine as long as the sync completes.
	if _, err := f.SyncIfOlder(gone, 0); err != nil && !errors.Is(err, ErrSyncPending) {
		t.Fatalf("SyncIfOlder with an ended context: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sync failed after its caller left: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("sync never finished after its caller left")
	}
	if syms, _ := db.FindSymbols(context.Background(), f.database, "B"); len(syms) == 0 {
		t.Error("b.go was not indexed by the sync its caller stopped waiting for")
	}
}

// After a failed sync the next ones are skipped for a while instead of every
// query retrying it.
func TestFailedSyncBacksOff(t *testing.T) {
	_, f, closeDB := newIndexedDir(t)
	var runs atomic.Int32
	f.OnSync = func(*scanner.IncrementalResult, error) { runs.Add(1) }
	closeDB() // every sync now fails

	ctx := context.Background()
	if _, err := f.SyncIfOlder(ctx, 0); err == nil {
		t.Fatal("sync on a closed database succeeded")
	}
	for i := 0; i < 5; i++ {
		if res, err := f.SyncIfOlder(ctx, 0); res != nil || err != nil {
			t.Fatalf("call %d during backoff: res=%v err=%v, want nil, nil", i, res, err)
		}
	}
	if n := runs.Load(); n != 1 {
		t.Errorf("%d sync passes ran, want 1 (the rest fall inside the retry delay)", n)
	}
}

func TestSyncRetryDelay(t *testing.T) {
	for failures, want := range map[int]time.Duration{
		1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 6: time.Minute, 50: time.Minute,
	} {
		if got := syncRetryDelay(failures); got != want {
			t.Errorf("syncRetryDelay(%d) = %v, want %v", failures, got, want)
		}
	}
}

// Several servers share one index. Only the one holding the rebuild lock may
// rebuild it; a lock left behind by a dead process stops counting.
func TestRebuildIfOutdatedTakesTheRebuildLock(t *testing.T) {
	dir, f, _ := newIndexedDir(t)
	ctx := context.Background()
	outdate := func() {
		t.Helper()
		if err := db.SetMeta(ctx, f.database, extractorVersionKey, "1"); err != nil {
			t.Fatal(err)
		}
	}
	lock := filepath.Join(dir, ".codive", "rebuild.lock")

	outdate()
	if err := os.WriteFile(lock, []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if rebuilt, err := RebuildIfOutdated(ctx, f.database, dir); err != nil || rebuilt {
		t.Fatalf("rebuilt while another process held the lock: rebuilt=%v err=%v", rebuilt, err)
	}
	if !NeedsReextract(ctx, f.database) {
		t.Fatal("index marked current although nothing rebuilt it")
	}

	old := time.Now().Add(-2 * rebuildLockStale)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if rebuilt, err := RebuildIfOutdated(ctx, f.database, dir); err != nil || !rebuilt {
		t.Fatalf("stale lock blocked the rebuild: rebuilt=%v err=%v", rebuilt, err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("rebuild lock left behind after the rebuild (stat err: %v)", err)
	}
}
