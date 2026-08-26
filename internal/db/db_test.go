package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDB(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ctxd_db_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, ".devctx", "index.db")
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
	tempDir, err := os.MkdirTemp("", "ctxd_migration_test_*")
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

	// 2. Open with ctxd db.Open (which automatically triggers Migrate)
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed to auto-migrate old database: %v", err)
	}
	defer database.Close()

	// Verify schema version is now CurrentSchemaVersion (3)
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


