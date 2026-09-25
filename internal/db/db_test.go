package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDB(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codive_db_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, ".codive", "index.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	if err := InitSchema(database); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC()
	sampleFiles := []FileRecord{
		{
			Path:         "main.go",
			Language:     "Go",
			SizeBytes:    500,
			ContentHash:  "hash1",
			LastModified: now.Add(-time.Hour),
			LastIndexed:  now,
		},
		{
			Path:         "util/math.go",
			Language:     "Go",
			SizeBytes:    300,
			ContentHash:  "hash2",
			LastModified: now.Add(-30 * time.Minute),
			LastIndexed:  now,
		},
		{
			Path:         "script.py",
			Language:     "Python",
			SizeBytes:    200,
			ContentHash:  "hash3",
			LastModified: now.Add(-10 * time.Minute),
			LastIndexed:  now,
		},
	}

	if err := SaveFiles(ctx, database, sampleFiles); err != nil {
		t.Fatalf("failed to save files: %v", err)
	}

	stats, err := GetStats(ctx, database)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}

	if stats.TotalFiles != 3 {
		t.Errorf("expected 3 files, got %d", stats.TotalFiles)
	}
	if stats.TotalSizeBytes != 1000 {
		t.Errorf("expected 1000 bytes, got %d", stats.TotalSizeBytes)
	}
	if stats.LanguageCounts["Go"] != 2 {
		t.Errorf("expected 2 Go files, got %d", stats.LanguageCounts["Go"])
	}
	if stats.LanguageCounts["Python"] != 1 {
		t.Errorf("expected 1 Python file, got %d", stats.LanguageCounts["Python"])
	}

	// Test GetAllFiles
	records, err := GetAllFiles(ctx, database)
	if err != nil {
		t.Fatalf("failed to GetAllFiles: %v", err)
	}
	if len(records) != 3 {
		t.Errorf("expected 3 records in GetAllFiles, got %d", len(records))
	}
	if records["main.go"].Language != "Go" {
		t.Errorf("expected main.go to be Go, got %s", records["main.go"].Language)
	}

	// Test DeleteFiles
	if err := DeleteFiles(ctx, database, []string{"script.py"}); err != nil {
		t.Fatalf("failed to delete file: %v", err)
	}

	recordsAfterDelete, err := GetAllFiles(ctx, database)
	if err != nil {
		t.Fatalf("failed to GetAllFiles after delete: %v", err)
	}
	if len(recordsAfterDelete) != 2 {
		t.Errorf("expected 2 records after delete, got %d", len(recordsAfterDelete))
	}
	if _, exists := recordsAfterDelete["script.py"]; exists {
		t.Errorf("expected script.py to be deleted from db")
	}

	// Test FTS Indexing & Search
	ftsFiles := map[string]string{
		"main.go":      "package main\n\nfunc main() {\n\tprintln(\"hello scanner\")\n}\n",
		"util/math.go": "package util\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
	}
	if err := SaveFTS(ctx, database, ftsFiles); err != nil {
		t.Fatalf("failed to save fts: %v", err)
	}

	results, err := SearchFTS(ctx, database, "scanner", 10)
	if err != nil {
		t.Fatalf("search fts failed: %v", err)
	}
	if len(results) != 1 || results[0].Path != "main.go" {
		t.Errorf("expected match in main.go, got %+v", results)
	}

	content, err := GetFileContent(ctx, database, "main.go")
	if err != nil || content != ftsFiles["main.go"] {
		t.Errorf("expected file content to match")
	}
}

func TestSchemaMigration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codive_migration_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "index.db")

	// 1. Manually create an old V1 database without symbols or file_fts
	rawDB, err := OpenRaw(dbPath)
	if err != nil {
		t.Fatalf("failed to open raw db: %v", err)
	}
	// Create only V1 schema
	v1Schema := `
	CREATE TABLE files (
		path TEXT PRIMARY KEY,
		language TEXT NOT NULL,
		size_bytes INTEGER NOT NULL,
		content_hash TEXT NOT NULL,
		last_modified TIMESTAMP NOT NULL,
		last_indexed TIMESTAMP NOT NULL
	);
	PRAGMA user_version = 1;
	`
	if _, err := rawDB.Exec(v1Schema); err != nil {
		rawDB.Close()
		t.Fatalf("failed to create v1 schema: %v", err)
	}
	rawDB.Close()

	// 2. Open with codive db.Open (which automatically triggers Migrate)
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed to auto-migrate old database: %v", err)
	}
	defer database.Close()

	// Verify schema version is now CurrentSchemaVersion
	version, err := GetSchemaVersion(database)
	if err != nil {
		t.Fatalf("failed to get schema version: %v", err)
	}
	if version != CurrentSchemaVersion {
		t.Errorf("expected version %d, got %d", CurrentSchemaVersion, version)
	}

	// 3. Verify that saving symbols and FTS operations succeed without error
	ctx := context.Background()
	symbols := []SymbolRecord{
		{FilePath: "main.go", Name: "Main", Kind: "function", Signature: "func Main()", LineNumber: 1},
	}
	if err := SaveSymbols(ctx, database, symbols); err != nil {
		t.Errorf("SaveSymbols failed on migrated db: %v", err)
	}

	ftsFiles := map[string]string{
		"main.go": "package main\n\nfunc Main() {}\n",
	}
	if err := SaveFTS(ctx, database, ftsFiles); err != nil {
		t.Errorf("SaveFTS failed on migrated db: %v", err)
	}

	searchResults, err := SearchFTS(ctx, database, "Main", 5)
	if err != nil {
		t.Errorf("SearchFTS failed on migrated db: %v", err)
	}
	if len(searchResults) != 1 {
		t.Errorf("expected 1 search result, got %d", len(searchResults))
	}
}

// FindTestsFor must list the test functions inside a matched test file, not
// just the file itself.
func TestFindTestsForListsTestNames(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	if err := SaveFiles(ctx, database, []FileRecord{
		{Path: "pkg/auth.go", Language: "Go", SizeBytes: 1, ContentHash: "a", LastModified: now, LastIndexed: now},
		{Path: "pkg/auth_test.go", Language: "Go", SizeBytes: 1, ContentHash: "b", LastModified: now, LastIndexed: now},
	}); err != nil {
		t.Fatalf("failed to save files: %v", err)
	}
	if err := SaveSymbols(ctx, database, []SymbolRecord{
		{FilePath: "pkg/auth_test.go", Name: "TestLogin", Kind: "function", Signature: "func TestLogin(t *testing.T)", LineNumber: 5},
		{FilePath: "pkg/auth_test.go", Name: "helper", Kind: "function", Signature: "func helper()", LineNumber: 9},
	}); err != nil {
		t.Fatalf("failed to save symbols: %v", err)
	}

	tests, err := FindTestsFor(ctx, database, "pkg/auth.go")
	if err != nil {
		t.Fatalf("FindTestsFor failed: %v", err)
	}
	if len(tests) != 1 || tests[0].TestFilePath != "pkg/auth_test.go" {
		t.Fatalf("expected pkg/auth_test.go, got %+v", tests)
	}
	if len(tests[0].TestNames) != 1 || tests[0].TestNames[0] != "TestLogin (L5)" {
		t.Errorf("expected [TestLogin (L5)], got %v", tests[0].TestNames)
	}
}

// A failure part-way through ApplyIndexChanges must roll back the whole batch,
// so a file's record never moves to a new hash without its symbols.
func TestApplyIndexChangesIsAtomic(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	if err := ApplyIndexChanges(ctx, database, IndexChanges{
		Files:   []FileRecord{{Path: "a.go", Language: "Go", SizeBytes: 1, ContentHash: "old", LastModified: now, LastIndexed: now}},
		Symbols: []SymbolRecord{{FilePath: "a.go", Name: "Old", Kind: "function", Signature: "func Old()", LineNumber: 1}},
		FTS:     map[string]string{"a.go": "func Old()"},
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	// Make the final (FTS) step fail after files and symbols were written.
	if _, err := database.Exec("DROP TABLE file_fts;"); err != nil {
		t.Fatalf("failed to drop fts: %v", err)
	}
	err = ApplyIndexChanges(ctx, database, IndexChanges{
		Files:   []FileRecord{{Path: "a.go", Language: "Go", SizeBytes: 2, ContentHash: "new", LastModified: now, LastIndexed: now}},
		Symbols: []SymbolRecord{{FilePath: "a.go", Name: "New", Kind: "function", Signature: "func New()", LineNumber: 1}},
		FTS:     map[string]string{"a.go": "func New()"},
	})
	if err == nil {
		t.Fatal("expected ApplyIndexChanges to fail without file_fts")
	}

	rec, _, _ := GetFile(ctx, database, "a.go")
	if rec.ContentHash != "old" {
		t.Errorf("file record changed despite failed batch: hash=%q", rec.ContentHash)
	}
	syms, _ := FindSymbolsInFile(ctx, database, "a.go")
	if len(syms) != 1 || syms[0].Name != "Old" {
		t.Errorf("symbols changed despite failed batch: %+v", syms)
	}
}

// A migration that fails part-way must leave neither its partial DDL nor a
// bumped schema version behind, so the next Open can simply retry it.
func TestMigrationIsAtomic(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	orig := Migrations
	t.Cleanup(func() { Migrations = orig })
	Migrations = append(append([]Migration{}, orig...), Migration{
		Version:     CurrentSchemaVersion + 1,
		Description: "deliberately fails after its first statement",
		SQL:         "CREATE TABLE half_applied (x INTEGER); SELECT * FROM no_such_table;",
	})

	if err := Migrate(database); err == nil {
		t.Fatal("expected the broken migration to fail")
	}
	if v, _ := GetSchemaVersion(database); v != CurrentSchemaVersion {
		t.Errorf("schema version moved to %d despite failed migration", v)
	}
	var n int
	if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'half_applied';").Scan(&n); err != nil {
		t.Fatalf("failed to inspect schema: %v", err)
	}
	if n != 0 {
		t.Error("partial DDL from failed migration was not rolled back")
	}
}

func TestWordMatching(t *testing.T) {
	tests := []struct {
		s, name            string
		wantWord, wantCall bool
	}{
		{"x := Scan(dir)", "Scan", true, true},
		{"x := ScanIncremental(dir)", "Scan", false, false},
		{"x := rescan(dir)", "scan", false, false},
		{"s.Scan(dir)", "Scan", true, true},
		{"var s Scanner", "Scan", false, false},
		{"// uses Scan here", "Scan", true, false},
		{"Scan", "Scan", true, false},
		{"save_decision(x)", "save_decision", true, true},
		{"save_decisions(x)", "save_decision", false, false},
	}
	for _, tt := range tests {
		if got := containsWord(tt.s, tt.name); got != tt.wantWord {
			t.Errorf("containsWord(%q, %q) = %v, want %v", tt.s, tt.name, got, tt.wantWord)
		}
		if got := containsCall(tt.s, tt.name); got != tt.wantCall {
			t.Errorf("containsCall(%q, %q) = %v, want %v", tt.s, tt.name, got, tt.wantCall)
		}
	}
}

// References and callers must match whole identifiers, and symbol search must
// treat '_' and '%' literally.
func TestReferencesMatchWholeIdentifiers(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	content := "package p\n\nfunc Scan() {}\n\nfunc ScanIncremental() {}\n\nfunc run() {\n\tScan()\n\tScanIncremental()\n}\n"
	if err := ApplyIndexChanges(ctx, database, IndexChanges{
		Files: []FileRecord{{Path: "p.go", Language: "Go", SizeBytes: int64(len(content)), ContentHash: "h", LastModified: now, LastIndexed: now}},
		Symbols: []SymbolRecord{
			{FilePath: "p.go", Name: "Scan", Kind: "function", Signature: "func Scan()", LineNumber: 3},
			{FilePath: "p.go", Name: "ScanIncremental", Kind: "function", Signature: "func ScanIncremental()", LineNumber: 5},
			{FilePath: "p.go", Name: "axb", Kind: "function", Signature: "func axb()", LineNumber: 12},
		},
		FTS: map[string]string{"p.go": content},
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	refs, err := FindReferences(ctx, database, "Scan", 50)
	if err != nil {
		t.Fatalf("FindReferences failed: %v", err)
	}
	var lines []int
	for _, r := range refs {
		lines = append(lines, r.LineNumber)
	}
	if len(lines) != 2 || lines[0] != 3 || lines[1] != 8 {
		t.Errorf("expected references to Scan on L3 and L8 only, got %v", lines)
	}

	callers, err := FindCallers(ctx, database, "Scan", 10)
	if err != nil {
		t.Fatalf("FindCallers failed: %v", err)
	}
	if len(callers) != 1 || callers[0].LineNumber != 8 {
		t.Errorf("expected one caller of Scan on L8, got %+v", callers)
	}

	if syms, _ := FindSymbols(ctx, database, "a_b"); len(syms) != 0 {
		t.Errorf("'_' was treated as a LIKE wildcard: %+v", syms)
	}
}

// FindCallees must keep its matching rules: functions count only when called,
// types count when mentioned, the signature line and the symbol itself are
// ignored, and the body ends at the next declaration in the file.
func TestFindCallees(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	main := "package p\n\nfunc Target(c *Config) Result {\n\tHelper()\n\t// OtherFunc is only mentioned\n\tv := Settings{}\n\tTarget(nil)\n\treturn v\n}\n\nfunc After() { Unrelated() }\n"
	syms := []SymbolRecord{
		{FilePath: "main.go", Name: "Target", Kind: "function", Signature: "func Target(c *Config) Result", LineNumber: 3},
		{FilePath: "main.go", Name: "After", Kind: "function", Signature: "func After()", LineNumber: 11},
		{FilePath: "lib.go", Name: "Helper", Kind: "function", Signature: "func Helper()", LineNumber: 3},
		{FilePath: "lib.go", Name: "OtherFunc", Kind: "function", Signature: "func OtherFunc()", LineNumber: 5},
		{FilePath: "lib.go", Name: "Unrelated", Kind: "function", Signature: "func Unrelated()", LineNumber: 7},
		{FilePath: "types.go", Name: "Settings", Kind: "struct", Signature: "type Settings struct", LineNumber: 3},
		{FilePath: "types.go", Name: "Config", Kind: "struct", Signature: "type Config struct", LineNumber: 5},
		{FilePath: "types.go", Name: "Result", Kind: "struct", Signature: "type Result struct", LineNumber: 7},
	}
	if err := ApplyIndexChanges(ctx, database, IndexChanges{
		Files: []FileRecord{
			{Path: "main.go", Language: "Go", SizeBytes: int64(len(main)), ContentHash: "m", LastModified: now, LastIndexed: now},
		},
		Symbols: syms,
		FTS:     map[string]string{"main.go": main},
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	callees, err := FindCallees(ctx, database, "Target")
	if err != nil {
		t.Fatalf("FindCallees failed: %v", err)
	}
	var got []string
	for _, c := range callees {
		got = append(got, c.Name)
	}
	if strings.Join(got, ",") != "Helper,Settings" {
		t.Errorf("expected callees [Helper Settings], got %v", got)
	}
}

// A capped reference page must report that more matches exist, and an
// uncapped one must not.
func TestFindReferencesPageReportsMore(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	content := "Hit()\nHit()\nHit()\n"
	if err := ApplyIndexChanges(ctx, database, IndexChanges{
		Files: []FileRecord{{Path: "a.go", Language: "Go", SizeBytes: 1, ContentHash: "h", LastModified: now, LastIndexed: now}},
		FTS:   map[string]string{"a.go": content},
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	page, err := FindReferencesPage(ctx, database, "Hit", 2)
	if err != nil || len(page.Refs) != 2 || !page.More {
		t.Errorf("limit 2 of 3: expected 2 refs with More, got %+v err=%v", page, err)
	}
	page, err = FindReferencesPage(ctx, database, "Hit", 3)
	if err != nil || len(page.Refs) != 3 || page.More {
		t.Errorf("limit 3 of 3: expected 3 refs without More, got %+v err=%v", page, err)
	}
	callers, err := FindCallersPage(ctx, database, "Hit", 1)
	if err != nil || len(callers.Refs) != 1 || !callers.More {
		t.Errorf("callers limit 1 of 3: expected More, got %+v err=%v", callers, err)
	}
}
