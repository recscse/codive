package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/recscse/codive/internal/db"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func setupRepo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "codive_git_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "config", "core.autocrlf", "false")

	if err := os.WriteFile(filepath.Join(dir, "tracked.go"), []byte("package sample\n\nfunc Existing() int { return 1 }\n"), 0644); err != nil {
		t.Fatalf("failed to write tracked.go: %v", err)
	}
	runGit(t, dir, "add", "tracked.go")
	runGit(t, dir, "commit", "-q", "-m", "init")

	return dir
}

// TestGetGitChanges_ModifiedFilePathNotTruncated regresses a bug where
// strings.TrimSpace on the whole `git status --porcelain` blob stripped the
// leading status-column space off the FIRST line only (porcelain lines are
// fixed-column "XY path", and X is often a literal space, e.g. " M" for
// "modified, not staged"). That shifted the fixed offset used to slice out
// the path and silently ate the file's first character (e.g. "tracked.go"
// came back as "racked.go").
func TestGetGitChanges_ModifiedFilePathNotTruncated(t *testing.T) {
	dir := setupRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "tracked.go"), []byte("package sample\n\nfunc Existing() int { return 1 }\n\nfunc AnotherOne() int {\n\treturn 2\n}\n"), 0644); err != nil {
		t.Fatalf("failed to modify tracked.go: %v", err)
	}

	dbPath := filepath.Join(dir, ".codive", "index.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	syms := []db.SymbolRecord{
		{FilePath: "tracked.go", Name: "Existing", Kind: "function", Signature: "func Existing() int", LineNumber: 3},
		{FilePath: "tracked.go", Name: "AnotherOne", Kind: "function", Signature: "func AnotherOne() int", LineNumber: 5},
	}
	if err := db.SaveSymbols(ctx, database, syms); err != nil {
		t.Fatalf("failed to save symbols: %v", err)
	}

	result, err := GetGitChanges(ctx, dir, database)
	if err != nil {
		t.Fatalf("GetGitChanges failed: %v", err)
	}

	var found *FileDiffSummary
	for i := range result.Files {
		if result.Files[i].Path == "tracked.go" {
			found = &result.Files[i]
		}
		if result.Files[i].Path == "racked.go" {
			t.Fatalf("regression: modified file path was truncated to %q", result.Files[i].Path)
		}
	}
	if found == nil {
		t.Fatalf("expected tracked.go in changes, got %+v", result.Files)
	}
	if found.Status != "modified" {
		t.Errorf("expected status 'modified', got %q", found.Status)
	}
}

// TestGetGitChanges_UntrackedFileReportsSymbols regresses two bugs: (1) an
// untracked (brand-new) file always reported 0 changed lines and no affected
// symbols because `git diff` produces no output for a file with nothing
// committed to compare against, and (2) even when a fallback path existed, it
// looked up symbols via a fuzzy name/signature search using the file's own
// path as the query string (db.FindSymbols(ctx, db, relPath)), which almost
// never matches anything — instead of db.FindSymbolsInFile, an exact lookup.
func TestGetGitChanges_UntrackedFileReportsSymbols(t *testing.T) {
	dir := setupRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "newfile.go"), []byte("package sample\n\nfunc BrandNew() int {\n\treturn 42\n}\n"), 0644); err != nil {
		t.Fatalf("failed to write newfile.go: %v", err)
	}

	dbPath := filepath.Join(dir, ".codive", "index.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	syms := []db.SymbolRecord{
		{FilePath: "newfile.go", Name: "BrandNew", Kind: "function", Signature: "func BrandNew() int", LineNumber: 3},
	}
	if err := db.SaveSymbols(ctx, database, syms); err != nil {
		t.Fatalf("failed to save symbols: %v", err)
	}

	result, err := GetGitChanges(ctx, dir, database)
	if err != nil {
		t.Fatalf("GetGitChanges failed: %v", err)
	}

	var found *FileDiffSummary
	for i := range result.Files {
		if result.Files[i].Path == "newfile.go" {
			found = &result.Files[i]
		}
	}
	if found == nil {
		t.Fatalf("expected newfile.go in changes, got %+v", result.Files)
	}
	if found.Status != "untracked" {
		t.Errorf("expected status 'untracked', got %q", found.Status)
	}
	if found.ChangedLineCount == 0 {
		t.Errorf("expected non-zero changed line count for a brand-new file")
	}
	if len(found.AffectedSymbols) != 1 || found.AffectedSymbols[0] != "[function] BrandNew (L3)" {
		t.Errorf("expected BrandNew to be reported as an affected symbol, got %+v", found.AffectedSymbols)
	}
}
