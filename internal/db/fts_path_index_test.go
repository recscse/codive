package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestIndex(t *testing.T) *sql.DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), ".codive", "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func fileChange(path, body, symbol string) IndexChanges {
	now := time.Now().UTC()
	return IndexChanges{
		Files:       []FileRecord{{Path: path, Language: "Go", SizeBytes: int64(len(body)), ContentHash: body, LastModified: now, LastIndexed: now}},
		Symbols:     []SymbolRecord{{FilePath: path, Name: symbol, Kind: "function", Signature: "func " + symbol + "()", LineNumber: 1}},
		FTS:         map[string]string{path: body},
		SymbolTerms: []SymbolTerms{{FilePath: path, Name: symbol, Kind: "function", LineNumber: 1, NameTerms: strings.ToLower(symbol), BodyTerms: body}},
	}
}

func queryPlan(t *testing.T, database *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain %q: %v", query, err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	return strings.Join(plan, "\n")
}

// Replacing or deleting one file's full-text rows must find them through the
// path index. Filtering an FTS table by its UNINDEXED path column reads every
// row, which on a large index made each sync hold the write lock for seconds
// however small the change was.
func TestFTSRowsAreFoundByPathIndex(t *testing.T) {
	database := openTestIndex(t)
	ctx := context.Background()
	if err := ApplyIndexChanges(ctx, database, fileChange("a.go", "alpha", "Alpha")); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ table, rowsByPath, index string }{
		{"file_fts", fileFTSRowsByPath, "idx_file_fts_path"},
		{"symbol_fts", symbolFTSRowsByPath, "idx_symbol_fts_path"},
	} {
		plan := queryPlan(t, database, "DELETE FROM "+tc.table+" WHERE rowid IN ("+tc.rowsByPath+" IN (?, ?));", "a.go", "b.go")
		if !strings.Contains(plan, tc.index) {
			t.Errorf("%s: deleting by path doesn't use %s:\n%s", tc.table, tc.index, plan)
		}
		// FTS5 reports a rowid lookup as index "0:=" and a full scan as "0:".
		if !strings.Contains(plan, tc.table+" VIRTUAL TABLE INDEX 0:=") {
			t.Errorf("%s: rows aren't deleted by rowid lookup:\n%s", tc.table, plan)
		}
	}
}

func TestApplyIndexChangesReplacesAndDeletesFullTextRows(t *testing.T) {
	database := openTestIndex(t)
	ctx := context.Background()
	apply := func(c IndexChanges) {
		t.Helper()
		if err := ApplyIndexChanges(ctx, database, c); err != nil {
			t.Fatalf("ApplyIndexChanges: %v", err)
		}
	}
	hits := func(word string) []string {
		t.Helper()
		res, err := SearchFTS(ctx, database, word, 10)
		if err != nil {
			t.Fatalf("SearchFTS(%s): %v", word, err)
		}
		var paths []string
		for _, r := range res {
			paths = append(paths, r.Path)
		}
		return paths
	}
	symbolHits := func(word string) int {
		t.Helper()
		res, err := SearchSymbols(ctx, database, []string{word}, 10)
		if err != nil {
			t.Fatalf("SearchSymbols(%s): %v", word, err)
		}
		return len(res)
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	apply(fileChange("a.go", "alpha", "Alpha"))
	apply(fileChange("b.go", "bravo", "Bravo"))

	// Modify a.go: its old rows go, b.go's stay.
	apply(fileChange("a.go", "charlie", "Charlie"))
	if got := hits("alpha"); len(got) != 0 {
		t.Errorf("old content of a.go still searchable: %v", got)
	}
	if got := hits("charlie"); len(got) != 1 || got[0] != "a.go" {
		t.Errorf("new content of a.go: got %v", got)
	}
	if got := hits("bravo"); len(got) != 1 || got[0] != "b.go" {
		t.Errorf("b.go was disturbed: got %v", got)
	}
	if symbolHits("alpha") != 0 || symbolHits("charlie") != 1 || symbolHits("bravo") != 1 {
		t.Errorf("symbol search rows not replaced: alpha=%d charlie=%d bravo=%d", symbolHits("alpha"), symbolHits("charlie"), symbolHits("bravo"))
	}
	if n := count("file_fts"); n != 2 {
		t.Errorf("file_fts has %d rows, want 2", n)
	}

	// Delete b.go.
	apply(IndexChanges{Deleted: []string{"b.go"}})
	if got := hits("bravo"); len(got) != 0 {
		t.Errorf("deleted file still searchable: %v", got)
	}
	if symbolHits("bravo") != 0 {
		t.Error("deleted file's symbols still searchable")
	}
	if n, m := count("file_fts"), count("symbol_fts"); n != 1 || m != 1 {
		t.Errorf("rows left: file_fts=%d symbol_fts=%d, want 1 and 1", n, m)
	}
	if content, err := GetFileContent(ctx, database, "a.go"); err != nil || content != "charlie" {
		t.Errorf("GetFileContent(a.go) = %q, %v", content, err)
	}
	if _, err := GetFileContent(ctx, database, "b.go"); err == nil {
		t.Error("GetFileContent(b.go) succeeded for a deleted file")
	}

	for _, table := range []string{"file_fts", "symbol_fts"} {
		if _, err := database.Exec("INSERT INTO " + table + "(" + table + ") VALUES('integrity-check');"); err != nil {
			t.Errorf("%s integrity check: %v", table, err)
		}
	}
}

// An index written before the path indexes existed has none. The first write
// must still work and leave them in place.
func TestFullTextPathIndexesAreAddedToAnOlderIndex(t *testing.T) {
	database := openTestIndex(t)
	ctx := context.Background()
	if err := ApplyIndexChanges(ctx, database, fileChange("a.go", "alpha", "Alpha")); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("DROP INDEX idx_file_fts_path; DROP INDEX idx_symbol_fts_path;"); err != nil {
		t.Fatalf("simulating an older index: %v", err)
	}
	if content, err := GetFileContent(ctx, database, "a.go"); err != nil || content != "alpha" {
		t.Errorf("lookup without the index: %q, %v", content, err)
	}

	if err := ApplyIndexChanges(ctx, database, fileChange("a.go", "bravo", "Bravo")); err != nil {
		t.Fatalf("write to an older index: %v", err)
	}
	var n int
	if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name IN ('idx_file_fts_path', 'idx_symbol_fts_path');").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("%d path indexes after a write, want 2", n)
	}
	if res, _ := SearchFTS(ctx, database, "alpha", 5); len(res) != 0 {
		t.Errorf("old content survived the write: %v", res)
	}
}
