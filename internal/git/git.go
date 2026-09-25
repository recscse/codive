// Package git provides native Git awareness and AST-aware diff summaries for codive.
package git

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/recscse/codive/internal/db"
)

// FileDiffSummary represents the AST-aware changes for a specific file.
type FileDiffSummary struct {
	Path             string   `json:"path"`
	Status           string   `json:"status"` // "modified", "added", "deleted", "untracked"
	AffectedSymbols  []string `json:"affected_symbols,omitempty"`
	ChangedLineCount int      `json:"changed_line_count"`
	// Note explains why this file's symbols and line count weren't analyzed,
	// so a skipped file is never mistaken for one with no changes.
	Note string `json:"note,omitempty"`
}

// GitChangesResult represents the overall repository git changes with AST context.
type GitChangesResult struct {
	Branch       string            `json:"branch"`
	TotalChanged int               `json:"total_changed"`
	Files        []FileDiffSummary `json:"files"`
	// Omitted counts changed files left out of Files because the working
	// tree has more than maxListedFiles changes.
	Omitted int `json:"omitted,omitempty"`
	// NotAnalyzed counts listed files past maxDetailedFiles, reported with
	// their status only.
	NotAnalyzed int `json:"not_analyzed,omitempty"`
}

func isLargeFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > maxNewFileBytes
}

const (
	// maxListedFiles bounds how many changed files are reported, so a huge
	// working tree (e.g. an un-ignored build directory) can't flood the
	// agent's context.
	maxListedFiles = 500
	// maxDetailedFiles bounds how many of those get symbol-level analysis;
	// the rest are listed with their status only.
	maxDetailedFiles = 300
	// maxNewFileBytes skips reading very large untracked files, matching the
	// scanner's own 5MB indexing limit.
	maxNewFileBytes = 5 * 1024 * 1024
)

var diffHunkRegex = regexp.MustCompile(`^@@\s+-\d+(?:,\d+)?\s+\+(\d+)(?:,(\d+))?\s+@@`)

// lineRange is an inclusive range of changed lines in the new file version.
type lineRange struct{ start, end int }

// gitCommand builds a git invocation that is killed when ctx ends and whose
// output pipes are released shortly after, so a stuck git can't hang a call.
func gitCommand(ctx context.Context, rootDir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = rootDir
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// GetGitChanges executes git status and diff, mapping changed line ranges to
// enclosing AST symbols. It runs a fixed number of git processes regardless
// of how many files changed.
func GetGitChanges(ctx context.Context, rootDir string, database *sql.DB) (*GitChangesResult, error) {
	branchOut, _ := gitCommand(ctx, rootDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		branch = "unknown"
	}

	// -z: NUL-separated entries with paths emitted verbatim. Without it git
	// C-quotes any path containing spaces or non-ASCII characters ("my file.go"
	// comes back wrapped in quotes with escapes), which then matches nothing in
	// the index. -uall: list each untracked file rather than collapsing a new
	// directory into a single "dir/" entry that can't be read or diffed.
	statusOut, err := gitCommand(ctx, rootDir, "status", "--porcelain=v1", "-z", "-uall").Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("git status timed out: %w", ctx.Err())
		}
		return &GitChangesResult{
			Branch:       "none (not a git repo)",
			TotalChanged: 0,
			Files:        nil,
		}, nil
	}
	entries := parseStatusZ(statusOut)
	result := &GitChangesResult{Branch: branch, TotalChanged: len(entries)}
	if len(entries) == 0 {
		return result, nil
	}

	hunks, err := diffHunks(ctx, rootDir)
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("git diff timed out: %w", ctx.Err())
	}

	for i, entry := range entries {
		if i >= maxListedFiles {
			result.Omitted = len(entries) - maxListedFiles
			break
		}
		statusName := "modified"
		if strings.Contains(entry.code, "?") {
			statusName = "untracked"
		} else if strings.Contains(entry.code, "A") {
			statusName = "added"
		} else if strings.Contains(entry.code, "D") {
			statusName = "deleted"
		}

		summary := FileDiffSummary{Path: entry.path, Status: statusName}
		switch {
		case i >= maxDetailedFiles:
			result.NotAnalyzed++
		case ctx.Err() != nil:
			summary.Note = "not analyzed: time limit reached"
		case statusName == "untracked" && isLargeFile(filepath.Join(rootDir, filepath.FromSlash(entry.path))):
			summary.Note = fmt.Sprintf("not analyzed: larger than %d MB", maxNewFileBytes/(1024*1024))
		default:
			if statusName == "untracked" {
				// git diff has nothing for untracked files (no committed or
				// staged version to compare against), so treat the whole file
				// as changed instead of reporting 0 lines.
				summary.AffectedSymbols, summary.ChangedLineCount = newFileAffectedSymbols(ctx, rootDir, entry.path, database)
			} else {
				summary.AffectedSymbols, summary.ChangedLineCount = rangesAffectedSymbols(ctx, database, entry.path, hunks[entry.path])
			}
		}
		result.Files = append(result.Files, summary)
	}
	return result, nil
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

// diffHunks returns the changed line ranges of every tracked file, from a
// single `git diff -U0 HEAD`. In a repository with no commits yet (no HEAD)
// it falls back to staged plus unstaged changes.
func diffHunks(ctx context.Context, rootDir string) (map[string][]lineRange, error) {
	base := []string{"-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff", "--unified=0"}
	hunks := make(map[string][]lineRange)
	err := streamDiff(ctx, rootDir, append(base, "HEAD", "--"), hunks)
	if err == nil || ctx.Err() != nil {
		return hunks, err
	}
	hunks = make(map[string][]lineRange)
	if err := streamDiff(ctx, rootDir, append(base, "--cached", "--"), hunks); err != nil {
		return hunks, err
	}
	return hunks, streamDiff(ctx, rootDir, append(base, "--"), hunks)
}

// streamDiff runs git diff and collects hunk ranges per file while streaming,
// so memory stays flat for huge diffs and over-long content lines (minified
// files) are skipped rather than buffered.
func streamDiff(ctx context.Context, rootDir string, args []string, hunks map[string][]lineRange) error {
	cmd := gitCommand(ctx, rootDir, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(stdout, 64*1024)
	current := ""
	for {
		line, isPrefix, err := reader.ReadLine()
		if err != nil {
			break
		}
		if isPrefix {
			// Only header lines matter and those are short; drain the rest of
			// an over-long content line.
			for isPrefix && err == nil {
				_, isPrefix, err = reader.ReadLine()
			}
			continue
		}
		switch {
		case bytes.HasPrefix(line, []byte("+++ ")):
			current = parseDiffPath(string(line[4:]))
		case current != "" && bytes.HasPrefix(line, []byte("@@")):
			if m := diffHunkRegex.FindSubmatch(line); m != nil {
				start, _ := strconv.Atoi(string(m[1]))
				count := 1
				if len(m[2]) > 0 {
					count, _ = strconv.Atoi(string(m[2]))
				}
				if count > 0 { // count 0 is a pure deletion: no new lines
					hunks[current] = append(hunks[current], lineRange{start, start + count - 1})
				}
			}
		}
	}
	return cmd.Wait()
}

// parseDiffPath extracts the path from a "+++ b/path" header value. It
// returns "" for /dev/null (a deleted file). Paths with unusual characters are
// C-quoted by git even with core.quotePath=false.
func parseDiffPath(v string) string {
	v = strings.TrimRight(v, "\t")
	if v == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(v, `"`) {
		if unq, err := strconv.Unquote(v); err == nil {
			v = unq
		}
	}
	return filepath.ToSlash(strings.TrimPrefix(v, "b/"))
}

// rangesAffectedSymbols maps changed line ranges to the enclosing declared
// symbols and returns them in line order with the total changed line count.
func rangesAffectedSymbols(ctx context.Context, database *sql.DB, relPath string, ranges []lineRange) ([]string, int) {
	changed := 0
	for _, r := range ranges {
		changed += r.end - r.start + 1
	}
	if changed == 0 || database == nil {
		return nil, changed
	}
	syms, err := db.FindSymbolsInFile(ctx, database, relPath)
	if err != nil || len(syms) == 0 {
		return nil, changed
	}

	// syms is ordered by line number. Symbol i owns the lines from its own
	// declaration up to the line before the next declaration, and is affected
	// if any changed range overlaps that span. Lines before the first
	// declaration belong to no symbol.
	var result []string
	for i, s := range syms {
		spanEnd := math.MaxInt
		if i+1 < len(syms) {
			spanEnd = syms[i+1].LineNumber - 1
		}
		for _, r := range ranges {
			if r.start <= spanEnd && r.end >= s.LineNumber {
				result = append(result, formatSymbol(s))
				break
			}
		}
	}
	return result, changed
}

func formatSymbol(s db.SymbolRecord) string {
	return fmt.Sprintf("[%s] %s (L%d)", s.Kind, s.Name, s.LineNumber)
}

// newFileAffectedSymbols reports every declared symbol in a brand-new
// (untracked) file, and its total line count, since the entire file is new
// and git diff has nothing to compare it against.
func newFileAffectedSymbols(ctx context.Context, rootDir string, relPath string, database *sql.DB) ([]string, int) {
	fullPath := filepath.Join(rootDir, filepath.FromSlash(relPath))
	if info, err := os.Stat(fullPath); err != nil || !info.Mode().IsRegular() || info.Size() > maxNewFileBytes {
		return nil, 0
	}
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
		if f.Note != "" {
			sb.WriteString(fmt.Sprintf("  (%s)\n", f.Note))
		}
		if len(f.AffectedSymbols) > 0 {
			sb.WriteString("  Enclosing Symbols Modified:\n")
			for _, sym := range f.AffectedSymbols {
				sb.WriteString(fmt.Sprintf("   - %s\n", sym))
			}
		}
	}

	if result.NotAnalyzed > 0 {
		sb.WriteString(fmt.Sprintf("\nNote: only the first %d changed files were analyzed for symbols; the last %d listed above show status only.\n", maxDetailedFiles, result.NotAnalyzed))
	}
	if result.Omitted > 0 {
		sb.WriteString(fmt.Sprintf("\n… %d more changed files not shown (listing is capped at %d). Run `git status` to see them all.\n", result.Omitted, maxListedFiles))
	}

	return strings.TrimSpace(sb.String())
}
