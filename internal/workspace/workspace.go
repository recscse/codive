// Package workspace decides which directory a codive index belongs to.
//
// One repository should have one index. Which directory an AI client launches
// the server in varies (the repository root, a package inside it, a parent of
// several repositories), so the server resolves the directory it is given
// instead of trusting it.
package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

// IndexDir is the directory inside a workspace that holds its index.
const IndexDir = ".codive"

// HasIndex reports whether dir holds a codive index.
func HasIndex(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, IndexDir, "index.db"))
	return err == nil
}

// IsGitRoot reports whether dir is the top of a git working tree (.git is a
// directory, or a file in worktrees and submodules).
func IsGitRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// Resolve returns the directory whose index serves dir:
//
//  1. the nearest directory at or above dir that already has an index, so a
//     repository indexed once is never indexed a second time from inside;
//  2. otherwise the root of the git working tree containing dir, so a new
//     index covers the whole repository and not whatever package the client
//     happened to start in;
//  3. otherwise dir itself.
//
// The result is absolute and cleaned.
func Resolve(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	abs = filepath.Clean(abs)

	gitRoot := ""
	for cur := abs; ; {
		if HasIndex(cur) {
			return cur
		}
		if gitRoot == "" && IsGitRoot(cur) {
			gitRoot = cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	if gitRoot != "" {
		return gitRoot
	}
	return abs
}

// skipDirs are directories never searched for nested indexes.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true,
	"build": true, "dist": true, ".venv": true, "venv": true, "__pycache__": true,
}

// NestedIndexes returns the directories below root, at most maxDepth levels
// down, that hold their own index. Such an index duplicates part of root's,
// and queries are answered from whichever one is nearest the directory the
// client started in.
func NestedIndexes(root string, maxDepth int) []string {
	var found []string
	root = filepath.Clean(root)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root {
			if skipDirs[d.Name()] || d.Name() == IndexDir {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(root, path)
			if strings.Count(filepath.ToSlash(rel), "/")+1 > maxDepth {
				return filepath.SkipDir
			}
			if HasIndex(path) {
				found = append(found, path)
			}
		}
		return nil
	})
	return found
}
