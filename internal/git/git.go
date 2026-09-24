// Package git provides native Git awareness and AST-aware diff summaries for codive.
package git

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/recscse/codive/internal/db"
)

// FileDiffSummary represents the AST-aware changes for a specific file.
type FileDiffSummary struct {
	Path             string   `json:"path"`
	Status           string   `json:"status"` // "modified", "added", "deleted", "untracked"
	AffectedSymbols  []string `json:"affected_symbols,omitempty"`
	ChangedLineCount int      `json:"changed_line_count"`
}

// GitChangesResult represents the overall repository git changes with AST context.
type GitChangesResult struct {
	Branch       string            `json:"branch"`
	TotalChanged int               `json:"total_changed"`
	Files        []FileDiffSummary `json:"files"`
}

var diffHunkRegex = regexp.MustCompile(`^@@\s+-\d+(?:,\d+)?\s+\+(\d+)(?:,(\d+))?\s+@@`)

// GetGitChanges executes git status and diff, mapping changed line ranges to enclosing AST symbols.
func GetGitChanges(ctx context.Context, rootDir string, database *sql.DB) (*GitChangesResult, error) {
	// Check current branch
	branchCmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = rootDir
	branchOut, _ := branchCmd.Output()
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		branch = "unknown"
	}

	// -z: NUL-separated entries with paths emitted verbatim. Without it git
	// C-quotes any path containing spaces or non-ASCII characters ("my file.go"
	// comes back wrapped in quotes with escapes), which then matches nothing in
	// the index. -uall: list each untracked file rather than collapsing a new
	// directory into a single "dir/" entry that can't be read or diffed.
	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v1", "-z", "-uall")
	statusCmd.Dir = rootDir
	statusOut, err := statusCmd.Output()
	if err != nil {
		return &GitChangesResult{
			Branch:       "none (not a git repo)",
			TotalChanged: 0,
			Files:        nil,
		}, nil
	}

	var summaries []FileDiffSummary
	for _, entry := range parseStatusZ(statusOut) {
		statusName := "modified"
		if strings.Contains(entry.code, "?") {
			statusName = "untracked"
		} else if strings.Contains(entry.code, "A") {
			statusName = "added"
		} else if strings.Contains(entry.code, "D") {
			statusName = "deleted"
		}

		affectedSyms, changedCount := getFileAffectedSymbols(ctx, rootDir, entry.path, database, statusName == "untracked")

		summaries = append(summaries, FileDiffSummary{
			Path:             entry.path,
			Status:           statusName,
			AffectedSymbols:  affectedSyms,
			ChangedLineCount: changedCount,
		})
	}

	return &GitChangesResult{
		Branch:       branch,
		TotalChanged: len(summaries),
		Files:        summaries,
	}, nil
}

type statusEntry struct {
	code string // two-character XY status, e.g. " M", "A ", "??", "R "
	path string // current path, slash-separated
}

// parseStatusZ parses `git status --porcelain=v1 -z` output. Each entry is
// "XY path" terminated by NUL; renames and copies are followed by one extra
// NUL-terminated field holding the original path, which is skipped.
func parseStatusZ(out []byte) []statusEntry {
	fields := strings.Split(string(out), "\x00")
	var entries []statusEntry
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		code := f[:2]
		entries = append(entries, statusEntry{code: code, path: filepath.ToSlash(f[3:])})
		if strings.ContainsAny(code, "RC") {
			i++ // skip the original path of the rename/copy
		}
	}
	return entries
}

func getFileAffectedSymbols(ctx context.Context, rootDir string, relPath string, database *sql.DB, isUntracked bool) ([]string, int) {
	if isUntracked {
		// git diff produces nothing for untracked files (there's no committed or
		// staged version to diff against), so without this they'd always report
		// 0 changed lines despite being entirely new. Treat the whole file as
		// changed instead.
		return newFileAffectedSymbols(ctx, rootDir, relPath, database)
	}

	diffCmd := exec.CommandContext(ctx, "git", "diff", "--unified=0", "HEAD", "--", filepath.FromSlash(relPath))
	diffCmd.Dir = rootDir
	diffOut, err := diffCmd.Output()
	if err != nil || len(diffOut) == 0 {
		// Fallback to unstaged diff
		diffCmd = exec.CommandContext(ctx, "git", "diff", "--unified=0", "--", filepath.FromSlash(relPath))
		diffCmd.Dir = rootDir
		diffOut, _ = diffCmd.Output()
	}

	if len(diffOut) == 0 {
		return nil, 0
	}

	// Parse changed line ranges from hunk headers: @@ -old,count +new,count @@
	var changedLines []int
	scanner := bufio.NewScanner(bytes.NewReader(diffOut))
	for scanner.Scan() {
		line := scanner.Text()
		if m := diffHunkRegex.FindStringSubmatch(line); len(m) > 1 {
			startLine, _ := strconv.Atoi(m[1])
			lineCount := 1
			if len(m) > 2 && m[2] != "" {
				lineCount, _ = strconv.Atoi(m[2])
			}
			for i := 0; i < lineCount; i++ {
				changedLines = append(changedLines, startLine+i)
			}
		}
	}

	if len(changedLines) == 0 || database == nil {
		return nil, len(changedLines)
	}

	// Fetch symbols declared in this exact file
	syms, err := db.FindSymbolsInFile(ctx, database, relPath)
	if err != nil || len(syms) == 0 {
		return nil, len(changedLines)
	}

	// Match changed lines against the closest preceding symbol declaration.
	// syms is ordered by line, so collecting indexes and walking them in order
	// keeps the output stable (it used to come out of a map in random order).
	matched := make(map[int]bool)
	for _, chLine := range changedLines {
		closest := -1
		for i, s := range syms {
			if s.LineNumber <= chLine && (closest < 0 || s.LineNumber > syms[closest].LineNumber) {
				closest = i
			}
		}
		if closest >= 0 {
			matched[closest] = true
		}
	}

	var result []string
	for i, s := range syms {
		if matched[i] {
			result = append(result, formatSymbol(s))
		}
	}
	return result, len(changedLines)
}

func formatSymbol(s db.SymbolRecord) string {
	return fmt.Sprintf("[%s] %s (L%d)", s.Kind, s.Name, s.LineNumber)
}

// newFileAffectedSymbols reports every declared symbol in a brand-new
// (untracked) file, and its total line count, since the entire file is new
// and git diff has nothing to compare it against.
func newFileAffectedSymbols(ctx context.Context, rootDir string, relPath string, database *sql.DB) ([]string, int) {
	fullPath := filepath.Join(rootDir, filepath.FromSlash(relPath))
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, 0
	}
	lineCount := strings.Count(string(content), "\n") + 1

	if database == nil {
		return nil, lineCount
	}

	syms, err := db.FindSymbolsInFile(ctx, database, relPath)
	if err != nil || len(syms) == 0 {
		return nil, lineCount
	}

	result := make([]string, 0, len(syms))
	for _, s := range syms {
		result = append(result, formatSymbol(s))
	}
	return result, lineCount
}

// FormatGitChanges returns a concise, token-efficient markdown report of the git changes.
func FormatGitChanges(result *GitChangesResult) string {
	if result == nil || result.TotalChanged == 0 {
		return "✓ Clean working tree: No uncommitted changes detected."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 🌿 Git Changes (%d files on branch `%s`)\n\n", result.TotalChanged, result.Branch))

	for _, f := range result.Files {
		sb.WriteString(fmt.Sprintf("• **%s** `[%s]`\n", f.Path, f.Status))
		if len(f.AffectedSymbols) > 0 {
			sb.WriteString("  Enclosing Symbols Modified:\n")
			for _, sym := range f.AffectedSymbols {
				sb.WriteString(fmt.Sprintf("   - %s\n", sym))
			}
		}
	}

	return strings.TrimSpace(sb.String())
}
