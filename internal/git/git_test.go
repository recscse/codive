package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func findFile(res *GitChangesResult, path string) *FileDiffSummary {
	for i := range res.Files {
		if res.Files[i].Path == path {
			return &res.Files[i]
		}
	}
	return nil
}

// Paths with spaces must come back verbatim (not C-quoted), files inside a
// new untracked directory must be listed individually, and a rename must be
// reported once under its new path.
func TestGetGitChanges_PorcelainEdgeCases(t *testing.T) {
	dir := setupRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "my file.go"), []byte("package sample\n"), 0644); err != nil {
		t.Fatalf("failed to write file with space: %v", err)
	}
	runGit(t, dir, "add", "my file.go")
	runGit(t, dir, "commit", "-q", "-m", "space")
	if err := os.WriteFile(filepath.Join(dir, "my file.go"), []byte("package sample\n\nfunc Spaced() {}\n"), 0644); err != nil {
		t.Fatalf("failed to modify file with space: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "newpkg"), 0755); err != nil {
		t.Fatalf("failed to create newpkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "newpkg", "fresh.go"), []byte("package newpkg\n\nfunc Fresh() {}\n"), 0644); err != nil {
		t.Fatalf("failed to write fresh.go: %v", err)
	}

	runGit(t, dir, "mv", "tracked.go", "renamed.go")

	res, err := GetGitChanges(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("GetGitChanges failed: %v", err)
	}

	if f := findFile(res, "my file.go"); f == nil || f.Status != "modified" || f.ChangedLineCount == 0 {
		t.Errorf("expected modified 'my file.go' with changed lines, got %+v (all: %+v)", f, res.Files)
	}
	if f := findFile(res, "newpkg/fresh.go"); f == nil || f.Status != "untracked" || f.ChangedLineCount != 4 {
		t.Errorf("expected untracked newpkg/fresh.go with 4 lines, got %+v (all: %+v)", f, res.Files)
	}
	if findFile(res, "newpkg/") != nil {
		t.Errorf("untracked directory reported as a single entry: %+v", res.Files)
	}
	if findFile(res, "renamed.go") == nil || findFile(res, "tracked.go") != nil {
		t.Errorf("expected rename reported once as renamed.go, got %+v", res.Files)
	}
	if res.TotalChanged != 3 {
		t.Errorf("expected 3 changed entries, got %d: %+v", res.TotalChanged, res.Files)
	}
}

// Affected symbols must be listed in line order, not map iteration order.
func TestGetGitChanges_AffectedSymbolsOrdered(t *testing.T) {
	dir := setupRepo(t)
	content := "package sample\n\nfunc A() {}\n\nfunc B() {}\n\nfunc C() {}\n\nfunc D() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "multi.go"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write multi.go: %v", err)
	}

	database, err := db.Open(filepath.Join(dir, ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()
	var syms []db.SymbolRecord
	for i, name := range []string{"A", "B", "C", "D"} {
		syms = append(syms, db.SymbolRecord{FilePath: "multi.go", Name: name, Kind: "function", Signature: "func " + name + "()", LineNumber: 3 + 2*i})
	}
	if err := db.SaveSymbols(context.Background(), database, syms); err != nil {
		t.Fatalf("failed to save symbols: %v", err)
	}

	want := "[function] A (L3),[function] B (L5),[function] C (L7),[function] D (L9)"
	for run := 0; run < 5; run++ {
		res, err := GetGitChanges(context.Background(), dir, database)
		if err != nil {
			t.Fatalf("GetGitChanges failed: %v", err)
		}
		f := findFile(res, "multi.go")
		if f == nil {
			t.Fatalf("multi.go missing from %+v", res.Files)
		}
		if got := strings.Join(f.AffectedSymbols, ","); got != want {
			t.Fatalf("run %d: symbols out of order:\n got  %s\n want %s", run, got, want)
		}
	}
}

// Past the listing and analysis caps, the result must say what was left out
// rather than silently dropping files or reporting them as unchanged.
func TestGetGitChanges_CapsAreReported(t *testing.T) {
	dir := setupRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "gen"), 0755); err != nil {
		t.Fatalf("failed to create gen: %v", err)
	}
	total := maxListedFiles + 5
	for i := 0; i < total; i++ {
		name := filepath.Join(dir, "gen", fmt.Sprintf("f%04d.go", i))
		if err := os.WriteFile(name, []byte("package gen\n"), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	res, err := GetGitChanges(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("GetGitChanges failed: %v", err)
	}
	if res.TotalChanged != total || len(res.Files) != maxListedFiles || res.Omitted != 5 {
		t.Errorf("expected total=%d listed=%d omitted=5, got total=%d listed=%d omitted=%d",
			total, maxListedFiles, res.TotalChanged, len(res.Files), res.Omitted)
	}
	if res.NotAnalyzed != maxListedFiles-maxDetailedFiles {
		t.Errorf("expected %d files listed without analysis, got %d", maxListedFiles-maxDetailedFiles, res.NotAnalyzed)
	}
	if res.Files[0].ChangedLineCount == 0 {
		t.Errorf("first file should have been analyzed: %+v", res.Files[0])
	}

	out := FormatGitChanges(res)
	if !strings.Contains(out, "5 more changed files not shown") || !strings.Contains(out, "status only") {
		t.Errorf("formatted output does not disclose the caps:\n%s", out[len(out)-400:])
	}
}
