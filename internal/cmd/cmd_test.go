package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/recscse/codive/internal/db"
)

func TestUpdateOnOutdatedSchema(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codive_update_compat_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create sample source file
	srcFile := filepath.Join(tempDir, "auth_service.py")
	if err := os.WriteFile(srcFile, []byte("def authenticate(user, pwd): return True\n"), 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// Create .codive directory with an old V1 schema (only files table, NO file_fts and NO symbols)
	codiveDir := filepath.Join(tempDir, ".codive")
	if err := os.MkdirAll(codiveDir, 0755); err != nil {
		t.Fatalf("failed to create .codive dir: %v", err)
	}
	dbPath := filepath.Join(codiveDir, "index.db")

	rawDB, err := db.OpenRaw(dbPath)
	if err != nil {
		t.Fatalf("failed to open raw db: %v", err)
	}
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

	// Modify the source file so update detects changes
	if err := os.WriteFile(srcFile, []byte("def authenticate(user, pwd): return False # updated\n"), 0644); err != nil {
		t.Fatalf("failed to modify source file: %v", err)
	}

	// Run codive update on this outdated database. It should auto-migrate (self-heal) and succeed!
	if err := RunUpdate(tempDir); err != nil {
		t.Fatalf("RunUpdate failed on outdated schema database: %v", err)
	}

	// Verify that search works on the updated database
	if err := RunSearch(tempDir, "authenticate", 5, false); err != nil {
		t.Fatalf("RunSearch failed after update: %v", err)
	}
}
