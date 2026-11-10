package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/recscse/codive/internal/ui"
)

// projectsFile lists every directory codive has indexed on this machine, one
// absolute path per line, so `codive clean` can show where disk space went.
func projectsFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codive", "projects.txt"), nil
}

func readProjects() ([]string, error) {
	path, err := projectsFile()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var projects []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			projects = append(projects, line)
		}
	}
	return projects, nil
}

func writeProjects(projects []string) error {
	path, err := projectsFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	sort.Strings(projects)
	return os.WriteFile(path, []byte(strings.Join(projects, "\n")+"\n"), 0o644)
}

// rememberProject records absDir as indexed. Failure only means `codive clean`
// won't list it, so it never stops indexing.
func rememberProject(absDir string) {
	projects, err := readProjects()
	if err != nil {
		return
	}
	for _, p := range projects {
		if samePath(p, absDir) {
			return
		}
	}
	_ = writeProjects(append(projects, absDir))
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// RunClean lists indexed projects and their index sizes, or deletes the index
// of one project when target is given. It only ever removes a directory named
// .codive that contains an index.db.
func RunClean(target string, assumeYes bool, in io.Reader) error {
	projects, err := readProjects()
	if err != nil {
		return fmt.Errorf("cannot read the project list: %w", err)
	}

	if target == "" {
		return listProjects(projects)
	}

	absDir, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}
	indexDir := filepath.Join(absDir, ".codive")
	if _, err := os.Stat(filepath.Join(indexDir, "index.db")); err != nil {
		return fmt.Errorf("no codive index found in %s", absDir)
	}

	size := dirSize(indexDir)
	if !assumeYes {
		fmt.Printf("Delete %s (%s)? The project's code is not touched. [y/N] ", indexDir, formatBytes(size))
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			ui.Info("Nothing was deleted.")
			return nil
		}
	}
	if err := os.RemoveAll(indexDir); err != nil {
		return fmt.Errorf("failed to delete %s: %w", indexDir, err)
	}

	kept := projects[:0]
	for _, p := range projects {
		if !samePath(p, absDir) {
			kept = append(kept, p)
		}
	}
	if err := writeProjects(kept); err != nil {
		ui.Warning(fmt.Sprintf("Index deleted, but the project list couldn't be updated: %v", err))
	}
	ui.Success(fmt.Sprintf("Deleted the index for %s and freed %s. Run codive init there to rebuild it.", absDir, formatBytes(size)))
	return nil
}

func listProjects(projects []string) error {
	fmt.Println()
	ui.Header("Indexed projects")
	if len(projects) == 0 {
		fmt.Println("  No projects recorded yet. Projects are listed after codive init runs in them.")
		fmt.Println()
		return nil
	}

	var total int64
	var gone []string
	for _, p := range projects {
		indexDir := filepath.Join(p, ".codive")
		if _, err := os.Stat(filepath.Join(indexDir, "index.db")); err != nil {
			gone = append(gone, p)
			continue
		}
		size := dirSize(indexDir)
		total += size
		fmt.Printf("  %10s  %s\n", formatBytes(size), p)
	}
	fmt.Printf("  %10s  total\n", formatBytes(total))
	fmt.Println()
	if len(gone) > 0 {
		var kept []string
		for _, p := range projects {
			if !containsPath(gone, p) {
				kept = append(kept, p)
			}
		}
		if err := writeProjects(kept); err == nil {
			ui.Info(fmt.Sprintf("Forgot %s whose index no longer exists.", ui.Count(len(gone), "project", "projects")))
		}
	}
	fmt.Println("  Delete one with: codive clean <path>")
	fmt.Println()
	return nil
}

func containsPath(paths []string, p string) bool {
	for _, q := range paths {
		if samePath(q, p) {
			return true
		}
	}
	return false
}
