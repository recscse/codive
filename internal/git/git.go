// Package git provides native Git awareness and AST-aware diff summaries for codive.
package git

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
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

	// Run git status --porcelain
	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	statusCmd.Dir = rootDir
	statusOut, err := statusCmd.Output()
	if err != nil {
		return &GitChangesResult{
			Branch:       "none (not a git repo)",
			TotalChanged: 0,
			Files:        nil,
		}, nil
	}

	lines := strings.Split(strings.TrimSpace(string(statusOut)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return &GitChangesResult{
			Branch:       branch,
			TotalChanged: 0,
			Files:        nil,
		}, nil
	}

	var summaries []FileDiffSummary
	for _, l := range lines {
		if len(l) < 4 {
			continue
		}
		statusCode := strings.TrimSpace(l[:2])
		filePath := strings.TrimSpace(l[3:])
		// Handle renamed files like "old -> new"
		if strings.Contains(filePath, " -> ") {
			parts := strings.Split(filePath, " -> ")
			filePath = parts[len(parts)-1]
		}
		filePath = filepath.ToSlash(filePath)

		statusName := "modified"
		if strings.Contains(statusCode, "?") {
			statusName = "untracked"
		} else if strings.Contains(statusCode, "A") {
			statusName = "added"
		} else if strings.Contains(statusCode, "D") {
			statusName = "deleted"
		}

		affectedSyms, changedCount := getFileAffectedSymbols(ctx, rootDir, filePath, database)

		summaries = append(summaries, FileDiffSummary{
			Path:             filePath,
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

func getFileAffectedSymbols(ctx context.Context, rootDir string, relPath string, database *sql.DB) ([]string, int) {
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

	// Fetch symbols for this file
	syms, err := db.FindSymbols(ctx, database, relPath)
	if err != nil || len(syms) == 0 {
		return nil, len(changedLines)
	}

	// Match changed lines against closest preceding symbol declaration
	matchedSymbols := make(map[string]bool)
	for _, chLine := range changedLines {
		var closestSym *db.SymbolRecord
		for _, s := range syms {
			if s.FilePath == relPath && s.LineNumber <= chLine {
				if closestSym == nil || s.LineNumber > closestSym.LineNumber {
					sCopy := s
					closestSym = &sCopy
				}
			}
		}
		if closestSym != nil {
			matchedSymbols[fmt.Sprintf("[%s] %s (L%d)", closestSym.Kind, closestSym.Name, closestSym.LineNumber)] = true
		}
	}

	var result []string
	for s := range matchedSymbols {
		result = append(result, s)
	}

	return result, len(changedLines)
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
