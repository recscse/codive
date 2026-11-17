package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func same(t *testing.T, got, want string) {
	t.Helper()
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestResolveUsesTheGitRootWhenThereIsNoIndexYet(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, ".git")
	pkg := mkdir(t, root, "CODING", "JAVA", "Main")
	same(t, Resolve(pkg), root)
	same(t, Resolve(root), root)
}

func TestResolvePrefersAnExistingIndexAboveTheDirectory(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, ".git")
	touch(t, filepath.Join(root, IndexDir, "index.db"))
	same(t, Resolve(mkdir(t, root, "a", "b")), root)
}

// The nearest index wins: someone who indexed a package on purpose keeps that
// index when they start the server inside it.
func TestResolveKeepsTheNearestIndex(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, ".git")
	touch(t, filepath.Join(root, IndexDir, "index.db"))
	inner := mkdir(t, root, "pkg")
	touch(t, filepath.Join(inner, IndexDir, "index.db"))
	same(t, Resolve(mkdir(t, inner, "deep")), inner)
	same(t, Resolve(mkdir(t, root, "other")), root)
}

func TestResolveFallsBackToTheDirectoryOutsideAnyRepository(t *testing.T) {
	dir := mkdir(t, t.TempDir(), "plain")
	same(t, Resolve(dir), dir)
}

func TestResolveTreatsAGitFileAsARoot(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, ".git")) // worktrees and submodules
	same(t, Resolve(mkdir(t, root, "src")), root)
}

func TestNestedIndexes(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, IndexDir, "index.db"))
	a := mkdir(t, root, "a")
	touch(t, filepath.Join(a, IndexDir, "index.db"))
	deep := mkdir(t, root, "x", "y", "z", "w")
	touch(t, filepath.Join(deep, IndexDir, "index.db"))
	touch(t, filepath.Join(root, "node_modules", "pkg", IndexDir, "index.db"))

	got := NestedIndexes(root, 3)
	if !reflect.DeepEqual(got, []string{a}) {
		t.Errorf("NestedIndexes = %v, want only %v (not the root's own, not beyond depth 3, not under node_modules)", got, a)
	}
}
