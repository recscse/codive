package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/scanner"
)

// extractorLanguages are the languages whose declarations are indexed. Every
// other language is searchable as text only, so a symbol in one of them is
// never found by find_symbol however it is spelled.
var extractorLanguages = map[string]bool{
	"Go": true, "TypeScript": true, "JavaScript": true, "Python": true,
	"Java": true, "C#": true, "Rust": true,
}

const maxNotFoundTextHits = 5

// symbolNotFound answers a find_symbol that matched nothing. A bare "no
// symbols found" reads as "the name does not exist", which is wrong whenever
// the index can't see it: the file may be in a language with no extractor,
// ignored, too large, or written in a way the line patterns miss. So the
// answer says what was searched, and lists plain-text occurrences of the name,
// which is how an agent finds the thing anyway.
func (s *Server) symbolNotFound(ctx context.Context, database *sql.DB, dir, query string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "No symbols found matching '%s'.\n\n", query)

	if stats, err := db.GetStats(ctx, database); err == nil && stats.TotalFiles > 0 {
		var withSymbols, textOnly []string
		nWith, nText := 0, 0
		for lang, n := range stats.LanguageCounts {
			label := fmt.Sprintf("%s (%d)", lang, n)
			if extractorLanguages[lang] {
				withSymbols = append(withSymbols, label)
				nWith += n
			} else {
				textOnly = append(textOnly, label)
				nText += n
			}
		}
		sort.Strings(withSymbols)
		sort.Strings(textOnly)
		fmt.Fprintf(&sb, "Searched the declaration index of %s.", countOf(stats.TotalFiles, "indexed file", "indexed files"))
		if len(withSymbols) > 0 {
			fmt.Fprintf(&sb, " Declarations are extracted from %d of them: %s.", nWith, strings.Join(withSymbols, ", "))
		}
		if len(textOnly) > 0 {
			fmt.Fprintf(&sb, " The other %s no symbol extractor and are searchable as text only: %s.", countOf(nText, "file has", "files have"), strings.Join(textOnly, ", "))
		}
		sb.WriteString("\n\n")
	}

	sb.WriteString("A miss here is not proof that the name doesn't exist. Declarations are found by pattern (Go excepted), so unusual formatting can be missed. If the conclusion matters, confirm with grep.\n\n")

	hits := s.textOccurrences(ctx, database, dir, query, maxNotFoundTextHits)
	if len(hits) == 0 {
		sb.WriteString("The name does not appear as text in the indexed files either. Files ignored by .gitignore or .codiveignore, binary files, files over 5 MB and credential files are not indexed.")
		return sb.String()
	}
	sb.WriteString("It does appear as text (these lines are not necessarily definitions):\n")
	for _, h := range hits {
		note := ""
		if lang := scanner.DetectLanguage(h.path); !extractorLanguages[lang] {
			note = fmt.Sprintf("  [%s: no symbol extractor]", lang)
		}
		fmt.Fprintf(&sb, "  %s L%d: %s%s\n", h.path, h.line, clipLine(h.text, h.col), note)
	}
	sb.WriteString("\nUse read_file_context on a path above, or search_code for every occurrence.")
	return sb.String()
}

func countOf(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

type textHit struct {
	path string
	line int
	col  int
	text string
}

// textOccurrences returns up to limit lines containing query (ignoring case)
// from the files the full-text index matches.
func (s *Server) textOccurrences(ctx context.Context, database *sql.DB, dir, query string, limit int) []textHit {
	files, err := db.SearchFTS(ctx, database, query, 10)
	if err != nil {
		return nil
	}
	var hits []textHit
	for _, f := range files {
		content, ok := s.searchableContent(ctx, database, dir, f.Path)
		if !ok {
			continue
		}
		for _, h := range matchingLines(content, strings.Fields(query), false) {
			// The index matches word stems; only lines holding the name as
			// typed are evidence that it exists.
			if !strings.Contains(strings.ToLower(h.text), strings.ToLower(query)) {
				continue
			}
			hits = append(hits, textHit{path: f.Path, line: h.line, col: h.col, text: h.text})
			if len(hits) >= limit {
				return hits
			}
		}
	}
	return hits
}
