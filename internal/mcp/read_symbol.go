package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/symbols"
)

const (
	// maxSymbolLines caps how much of one definition read_symbol returns; a
	// large class is better explored with get_file_skeleton.
	maxSymbolLines = 400
	// maxFreshnessChecks bounds how many candidate files are re-checked on
	// disk before answering.
	maxFreshnessChecks = 20
)

// nonCodeKinds are indexed symbols that aren't code definitions (build
// manifests, annotations) and so have no body to show.
var nonCodeKinds = map[string]bool{
	"annotation": true, "dependency": true, "devDependency": true,
	"package": true, "project": true, "parent_pom": true,
}

var readSymbolTool = Tool{
	Name:        "read_symbol",
	Description: "Returns the exact source of one function, method, class, or type: its doc comment, signature, and full body, without the rest of the file. Use it instead of reading whole files when you know what you need. Accepts `Name`, `Type.Method`, or `path/to/file.go:Name`.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"symbol": map[string]any{
				"type":        "string",
				"description": "Symbol to read: `Name`, `Type.Method`, or `path:Name`",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional file path to choose between definitions with the same name",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of matching definitions to return (default 3)",
			},
			"workspace_path": map[string]any{
				"type":        "string",
				"description": "Optional path to target repository root",
			},
		},
		"required": []string{"symbol"},
	},
}

// symbolQuery is a parsed read_symbol request.
type symbolQuery struct {
	path      string // file filter, slash-separated
	qualifier string // enclosing type, e.g. "Server" in "Server.Serve"
	name      string
}

func parseSymbolQuery(q, pathArg string) symbolQuery {
	q = strings.TrimSpace(strings.ReplaceAll(q, "::", "."))
	var sq symbolQuery
	if i := strings.LastIndex(q, ":"); i > 0 && strings.ContainsAny(q[:i], "./\\") {
		sq.path, q = q[:i], q[i+1:]
	}
	if strings.TrimSpace(pathArg) != "" {
		sq.path = pathArg
	}
	sq.path = strings.TrimPrefix(filepath.ToSlash(sq.path), "./")
	if i := strings.LastIndex(q, "."); i > 0 {
		sq.qualifier, q = q[:i], q[i+1:]
		if j := strings.LastIndex(sq.qualifier, "."); j >= 0 {
			sq.qualifier = sq.qualifier[j+1:]
		}
	}
	sq.name = q
	return sq
}

func (s *Server) readSymbol(ctx context.Context, targetDB *sql.DB, targetDir string, args map[string]any) (*ToolCallResult, error) {
	raw, _ := args["symbol"].(string)
	pathArg, _ := args["path"].(string)
	sq := parseSymbolQuery(raw, pathArg)
	if sq.name == "" {
		return nil, fmt.Errorf("symbol argument is required")
	}
	limit := 3
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	files := newFileCache(targetDir)
	matches, err := s.matchSymbols(ctx, targetDB, sq, files)
	if err != nil {
		return nil, err
	}
	// Re-check candidate files on disk; if any changed, match again so line
	// numbers and bodies are current.
	refreshed := false
	seen := make(map[string]bool)
	for _, m := range matches {
		if !seen[m.FilePath] && len(seen) < maxFreshnessChecks {
			seen[m.FilePath] = true
			if s.ensureFreshSymbols(ctx, targetDB, targetDir, m.FilePath) {
				refreshed = true
			}
		}
	}
	if refreshed {
		files = newFileCache(targetDir)
		if matches, err = s.matchSymbols(ctx, targetDB, sq, files); err != nil {
			return nil, err
		}
	}

	if len(matches) == 0 {
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: noSymbolMessage(ctx, targetDB, sq)}}}, nil
	}

	var sb strings.Builder
	rawTokens := 0
	shown := matches[:min(limit, len(matches))]
	for i, m := range shown {
		if i > 0 {
			sb.WriteString("\n")
		}
		f, err := files.get(m.FilePath)
		if err != nil {
			sb.WriteString(fmt.Sprintf("### `%s` — `%s:%d`\n(could not read file: %v)\n", m.Name, m.FilePath, m.LineNumber, err))
			continue
		}
		rawTokens += estimateTokens(f.text)
		start, end, _ := symbols.SymbolExtent(f.lang, f.content, m, nextDeclLine(f.syms, m.LineNumber))
		total := end - start + 1
		cut := end
		if total > maxSymbolLines {
			cut = start + maxSymbolLines - 1
		}
		sb.WriteString(fmt.Sprintf("### `%s` [%s] — `%s` L%d-%d (%d of %d lines in file)\n", m.Name, m.Kind, m.FilePath, start, end, total, len(f.lines)))
		sb.WriteString("```" + fenceLanguage(f.lang) + "\n")
		sb.WriteString(strings.Join(f.lines[start-1:cut], "\n"))
		sb.WriteString("\n```\n")
		if cut < end {
			sb.WriteString(fmt.Sprintf("[Showing the first %d of %d lines. Continue with read_file_context path=%q start_line=%d, or use get_file_skeleton for an outline.]\n", maxSymbolLines, total, m.FilePath, cut+1))
		}
	}
	if len(matches) > len(shown) {
		sb.WriteString(fmt.Sprintf("\n%d more definition(s) named `%s` — pass `path` (or `Type.%s`) to pick one:\n", len(matches)-len(shown), sq.name, sq.name))
		for _, m := range matches[len(shown):min(len(matches), len(shown)+20)] {
			sb.WriteString(fmt.Sprintf("- `%s:%d` [%s]\n", m.FilePath, m.LineNumber, m.Kind))
		}
		if len(matches) > len(shown)+20 {
			sb.WriteString(fmt.Sprintf("- … and %d more\n", len(matches)-len(shown)-20))
		}
	}

	text := sb.String()
	db.RecordTelemetry(ctx, targetDB, "read_symbol", estimateTokens(text), rawTokens, 3)
	return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: text}}}, nil
}

// matchSymbols finds the code definitions matching the query.
func (s *Server) matchSymbols(ctx context.Context, database *sql.DB, sq symbolQuery, files *fileCache) ([]db.SymbolRecord, error) {
	cands, err := db.FindSymbolsByName(ctx, database, sq.name)
	if err != nil {
		return nil, err
	}
	var out []db.SymbolRecord
	for _, c := range cands {
		if nonCodeKinds[c.Kind] {
			continue
		}
		if sq.path != "" && c.FilePath != sq.path && !strings.HasSuffix(c.FilePath, "/"+sq.path) {
			continue
		}
		if sq.qualifier != "" && !enclosedBy(c, sq.qualifier, files) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// enclosedBy reports whether sym belongs to the type named qualifier: either
// the type appears in its signature (a Go receiver such as `(s *Server)`), or
// sym sits inside the extent of a type declaration with that name in the
// same file (a class method in Python, Java, TypeScript, ...).
func enclosedBy(sym db.SymbolRecord, qualifier string, files *fileCache) bool {
	if recv, ok := goReceiverType(sym.Signature); ok {
		// Exact type name: a substring test would let `Decoder.Decode`
		// match `(m moveToFrontDecoder) Decode`.
		return recv == qualifier
	}
	f, err := files.get(sym.FilePath)
	if err != nil {
		return false
	}
	for _, t := range f.syms {
		if t.Name != qualifier || t.LineNumber >= sym.LineNumber {
			continue
		}
		switch t.Kind {
		case "class", "struct", "interface", "record", "enum", "trait", "impl", "type":
			_, end, _ := symbols.SymbolExtent(f.lang, f.content, t, 0)
			if end >= sym.LineNumber {
				return true
			}
		}
	}
	return false
}

// goReceiverType returns the receiver type name of a Go method signature
// such as `func (s *Server[T]) Start()` ("Server"). ok is false for anything
// that isn't a Go method signature.
func goReceiverType(sig string) (string, bool) {
	if !strings.HasPrefix(sig, "func (") {
		return "", false
	}
	end := strings.Index(sig, ")")
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(sig[len("func ("):end])
	if len(fields) == 0 {
		return "", false
	}
	t := strings.TrimLeft(fields[len(fields)-1], "*")
	if i := strings.IndexByte(t, '['); i >= 0 {
		t = t[:i]
	}
	return t, t != ""
}

func nextDeclLine(syms []db.SymbolRecord, line int) int {
	next := 0
	for _, s := range syms {
		if s.LineNumber > line && (next == 0 || s.LineNumber < next) {
			next = s.LineNumber
		}
	}
	return next
}

// noSymbolMessage explains a miss and suggests close names, so the agent can
// retry instead of falling back to reading whole files.
func noSymbolMessage(ctx context.Context, database *sql.DB, sq symbolQuery) string {
	msg := fmt.Sprintf("No definition named `%s`", sq.name)
	if sq.qualifier != "" {
		msg += fmt.Sprintf(" inside `%s`", sq.qualifier)
	}
	if sq.path != "" {
		msg += fmt.Sprintf(" in `%s`", sq.path)
	}
	msg += "."
	similar, _ := db.FindSymbols(ctx, database, sq.name)
	var names []string
	seen := make(map[string]bool)
	for _, s := range similar {
		key := s.Name + " (" + s.FilePath + ")"
		if nonCodeKinds[s.Kind] || seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, fmt.Sprintf("`%s` in `%s`", s.Name, s.FilePath))
		if len(names) == 8 {
			break
		}
	}
	if len(names) > 0 {
		msg += " Similar definitions: " + strings.Join(names, ", ") + "."
	} else {
		msg += " Use search_code for text that isn't a declared symbol."
	}
	return msg
}

// fileCache reads each file (and its indexed symbols) once per request.
type fileCache struct {
	root  string
	files map[string]*cachedFile
}

type cachedFile struct {
	content []byte
	text    string
	lines   []string
	lang    string
	syms    []db.SymbolRecord
}

func newFileCache(root string) *fileCache {
	return &fileCache{root: root, files: make(map[string]*cachedFile)}
}

func (c *fileCache) get(relPath string) (*cachedFile, error) {
	if f, ok := c.files[relPath]; ok {
		return f, nil
	}
	full, err := validateSafeRelPath(c.root, relPath)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	lang := scanner.DetectLanguage(relPath)
	syms, _ := symbols.ExtractSymbols(relPath, lang, content)
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	f := &cachedFile{content: content, text: text, lines: strings.Split(text, "\n"), lang: lang, syms: syms}
	c.files[relPath] = f
	return f, nil
}

func fenceLanguage(lang string) string {
	switch lang {
	case "C#":
		return "csharp"
	case "C++":
		return "cpp"
	case "TypeScript":
		return "ts"
	case "JavaScript":
		return "js"
	case "Text":
		return ""
	}
	return strings.ToLower(lang)
}
