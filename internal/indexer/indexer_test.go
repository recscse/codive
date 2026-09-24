package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
