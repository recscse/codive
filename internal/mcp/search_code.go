package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/recscse/codive/internal/db"
)

const (
	// defaultSearchLinesPerFile caps the matching lines listed for one file
	// unless the caller asks for more; the rest are counted, never dropped
	// silently.
	defaultSearchLinesPerFile = 10
	// maxSearchLineChars caps one listed line.
	maxSearchLineChars = 240
)

// searchCode answers search_code: the full-text index finds the files, and
// every matching line in them is listed with its line number. A file used to
// be reported once, with a single snippet and no line number, and the count
// in the heading was files rather than occurrences, so three TODOs in one
// file read as one match.
func (s *Server) searchCode(ctx context.Context, targetDB *sql.DB, targetDir string, args map[string]any) (*ToolCallResult, error) {
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query argument is required")
	}
	limit := 20
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	perFile := defaultSearchLinesPerFile
	if v, ok := args["max_lines_per_file"].(float64); ok && v > 0 {
		perFile = int(v)
	}

	results, err := db.SearchFTS(ctx, targetDB, query, limit)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No matches found for '%s'", query)}},
		}, nil
	}
	totalFiles, err := db.CountFTSMatches(ctx, targetDB, query)
	if err != nil || totalFiles < len(results) {
		totalFiles = len(results)
	}

	words, phrase := db.SplitSearchQuery(query)
	var body strings.Builder
	totalLines := 0
	for _, res := range results {
		content, ok := s.searchableContent(ctx, targetDB, targetDir, res.Path)
		var hits []lineHit
		if ok {
			hits = matchingLines(content, words, phrase)
		}
		totalLines += len(hits)
		if len(hits) == 0 {
			// The index matches word stems ("running" finds "run") and
			// ignores punctuation, so a file can match without any line
			// containing the words as typed. Show the index's own excerpt.
			body.WriteString(fmt.Sprintf("**%s** (matched by word stem; excerpt)\n", res.Path))
			excerpt := strings.NewReplacer(">>>", "[", "<<<", "]").Replace(res.Snippet)
			for _, l := range strings.Split(excerpt, "\n") {
				body.WriteString("  " + clipLine(strings.TrimSpace(l), 0) + "\n")
			}
			body.WriteString("\n")
			continue
		}
		body.WriteString(fmt.Sprintf("**%s** (%d)\n", res.Path, len(hits)))
		for _, h := range hits[:min(perFile, len(hits))] {
			body.WriteString(fmt.Sprintf("  L%d: %s\n", h.line, clipLine(h.text, h.col)))
		}
		if len(hits) > perFile {
			body.WriteString(fmt.Sprintf("  … %d more matching lines in this file (raise `max_lines_per_file` to list them)\n", len(hits)-perFile))
		}
		body.WriteString("\n")
	}

	var sb strings.Builder
	kind := "all words anywhere in a file"
	if phrase {
		kind = "exact phrase"
	}
	if totalFiles > len(results) {
		sb.WriteString(fmt.Sprintf("Showing %d of %d matching files for '%s' (%s): %d matching line(s) in these. MORE FILES MATCH; raise `limit` or narrow the query.\n\n",
			len(results), totalFiles, query, kind, totalLines))
	} else {
		sb.WriteString(fmt.Sprintf("Found %d matching line(s) in %d file(s) for '%s' (%s):\n\n", totalLines, len(results), query, kind))
	}
	sb.WriteString(body.String())

	used := estimateTokens(sb.String())
	db.RecordTelemetry(ctx, targetDB, "search_code", used, 0)
	return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: sb.String()}}}, nil
}

// searchableContent returns a file's current text: from disk, so line numbers
// are right even if the index is a moment behind, falling back to the copy in
// the index.
func (s *Server) searchableContent(ctx context.Context, targetDB *sql.DB, targetDir, relPath string) (string, bool) {
	if full, err := validateSafeRelPath(targetDir, relPath); err == nil {
		if b, err := os.ReadFile(full); err == nil {
			return string(b), true
		}
	}
	if content, err := db.GetFileContent(ctx, targetDB, relPath); err == nil {
		return content, true
	}
	return "", false
}

type lineHit struct {
	line int // 1-based
	col  int // byte offset of the first match in text
	text string
}

// matchingLines returns the lines of content that match the query, ignoring
// case. A phrase must appear as typed (runs of whitespace aside). Otherwise a
// line matches when it holds every word; if no line in the file does, the
// words are spread over several lines and each line holding any of them is
// returned.
func matchingLines(content string, words []string, phrase bool) []lineHit {
	if len(words) == 0 {
		return nil
	}
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	needle := strings.Join(lower, " ")

	var all, any []lineHit
	for i, raw := range strings.Split(content, "\n") {
		text := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if text == "" {
			continue
		}
		low := strings.ToLower(text)
		if phrase {
			if col := strings.Index(strings.Join(strings.Fields(low), " "), needle); col >= 0 {
				all = append(all, lineHit{line: i + 1, col: max(strings.Index(low, lower[0]), 0), text: text})
			}
			continue
		}
		first, found := -1, 0
		for _, w := range lower {
			if col := strings.Index(low, w); col >= 0 {
				found++
				if first < 0 || col < first {
					first = col
				}
			}
		}
		switch {
		case found == len(lower):
			all = append(all, lineHit{line: i + 1, col: first, text: text})
		case found > 0:
			any = append(any, lineHit{line: i + 1, col: first, text: text})
		}
	}
	if len(all) > 0 || phrase {
		return all
	}
	return any
}

// clipLine shortens a long line to maxSearchLineChars around byte offset col,
// marking what was cut.
func clipLine(text string, col int) string {
	if len(text) <= maxSearchLineChars {
		return text
	}
	start := max(col-maxSearchLineChars/3, 0)
	end := min(start+maxSearchLineChars, len(text))
	start = max(end-maxSearchLineChars, 0)
	for start > 0 && start < len(text) && text[start]&0xC0 == 0x80 {
		start++
	}
	for end < len(text) && text[end]&0xC0 == 0x80 {
		end--
	}
	out := text[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}
