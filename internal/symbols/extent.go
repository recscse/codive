package symbols

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/recscse/codive/internal/db"
)

// maxLeadingContextLines bounds how far above a declaration its doc comment,
// decorators, and annotations are searched for.
const maxLeadingContextLines = 40

// SymbolExtent returns the 1-based inclusive line range of sym's full
// definition in content: its leading doc comment, decorators, or annotations,
// its header, and its whole body. nextDecl is the line of the next symbol
// declared in the same file (0 if none); it's only used as a fallback when
// the language-specific boundary can't be determined. exact reports whether
// the range came from a real parser (Go) rather than a lexical heuristic.
func SymbolExtent(language string, content []byte, sym db.SymbolRecord, nextDecl int) (start, end int, exact bool) {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	decl := sym.LineNumber
	if decl < 1 || decl > len(lines) {
		return 0, 0, false
	}

	if language == "Go" {
		if s, e, ok := goExtent(content, decl); ok {
			return s, e, true
		}
	}

	end = 0
	switch language {
	case "Python":
		end = indentExtent(lines, decl)
	case "Go", "JavaScript", "TypeScript", "Java", "C#", "Rust", "C", "C++", "Kotlin", "Swift", "PHP", "Scala":
		end = braceExtent(lines, decl, language)
	}
	if end == 0 {
		end = fallbackExtent(lines, decl, nextDecl)
	}
	return leadingContextStart(lines, decl, language), end, false
}

// goExtent finds the exact extent of the Go declaration starting on line decl.
func goExtent(content []byte, decl int) (int, int, bool) {
	ext, ok := goExtents(content)[decl]
	return ext[0], ext[1], ok
}

// goExtents parses a Go file once and maps each function, method, and type
// declaration's line to its exact [start, end] lines (start includes the doc
// comment).
func goExtents(content []byte) map[int][2]int {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, parser.ParseComments)
	if err != nil {
		return nil
	}
	line := func(p token.Pos) int { return fset.Position(p).Line }
	startWithDoc := func(doc *ast.CommentGroup, pos token.Pos) int {
		if doc != nil {
			return line(doc.Pos())
		}
		return line(pos)
	}
	out := make(map[int][2]int)
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			out[line(d.Pos())] = [2]int{startWithDoc(d.Doc, d.Pos()), line(d.End())}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if !d.Lparen.IsValid() {
					// `type X struct {...}`: the declaration is the whole GenDecl.
					out[line(ts.Pos())] = [2]int{startWithDoc(d.Doc, d.Pos()), line(d.End())}
				} else {
					// One spec inside a grouped `type ( ... )` block.
					out[line(ts.Pos())] = [2]int{startWithDoc(ts.Doc, ts.Pos()), line(ts.End())}
				}
			}
		}
	}
	return out
}

// indentExtent returns the last line of a Python block whose header is on
// line decl: the last non-blank line indented deeper than the header.
func indentExtent(lines []string, decl int) int {
	base := indentOf(lines[decl-1])
	end := decl
	for i := decl; i < len(lines); i++ {
		l := lines[i]
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		if indentOf(l) > base {
			end = i + 1
			continue
		}
		// A comment at a shallower indent doesn't end the block by itself,
		// but it isn't part of it unless deeper code follows.
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		break
	}
	return end
}

func indentOf(l string) int {
	n := 0
	for _, c := range l {
		switch c {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// braceExtent returns the line holding the brace that closes the body opened
// after the declaration on line decl, skipping braces inside strings,
// comments, and character literals. It returns the declaration's own end for
// bodyless declarations (e.g. `void run();` in an interface) and 0 when no
// body can be found.
func braceExtent(lines []string, decl int, language string) int {
	const maxHeaderLines = 20
	depth := 0
	parens := 0       // ( and [ nesting in the header, before the body opens
	headerBraces := 0 // { } nesting inside those parens
	opened := false
	openLine := -1   // 0-based line where the current body brace opened
	inBlock := false // inside /* ... */
	var quote byte   // inside a string delimited by quote
	declIndent := indentOf(lines[decl-1])
	for i := decl - 1; i < len(lines); i++ {
		l := lines[i]
		if !opened && i > decl-1 && !inBlock && quote == 0 && depth == 0 && parens == 0 {
			if i-(decl-1) >= maxHeaderLines {
				return 0
			}
			// Before any body opens, a blank line or a new statement at the
			// declaration's own indentation means it had no body at all
			// (`type Format int`, `type X = number`): end at the previous
			// line instead of swallowing the next declaration's braces.
			t := strings.TrimSpace(l)
			if t == "" || (indentOf(l) <= declIndent && !isHeaderContinuation(t)) {
				return lastNonBlank(lines, decl, i)
			}
		}
		for j := 0; j < len(l); j++ {
			c := l[j]
			switch {
			case inBlock:
				if c == '*' && j+1 < len(l) && l[j+1] == '/' {
					inBlock = false
					j++
				}
			case quote != 0:
				// Backslash escapes, except in raw strings: Go's backtick
				// strings have none (JS/TS template literals do).
				rawString := quote == '`' && language != "JavaScript" && language != "TypeScript"
				if c == '\\' && !rawString {
					j++
				} else if c == quote {
					quote = 0
				}
			case c == '/' && j+1 < len(l) && l[j+1] == '/':
				j = len(l) // line comment
			case c == '#' && language == "PHP":
				j = len(l)
			case c == '/' && j+1 < len(l) && l[j+1] == '*':
				inBlock = true
				j++
			case c == '"' || c == '`':
				quote = c
			case c == '\'':
				if isCharLiteral(l, j) || language == "JavaScript" || language == "TypeScript" || language == "PHP" {
					quote = c
				}
				// Otherwise a Rust lifetime or Kotlin/Swift label: not a string.
			case !opened && (c == '(' || c == '['):
				parens++
			case !opened && (c == ')' || c == ']'):
				parens--
			case c == '{' && !opened && parens > 0:
				// A brace inside the header's parens or brackets: parameter
				// destructuring (`function Card({ title }: Props)`), an
				// inline object type, or a generic constraint. Not the body.
				headerBraces++
			case c == '}' && !opened && headerBraces > 0:
				headerBraces--
			case c == '{':
				if !opened {
					openLine = i
				}
				depth++
				opened = true
			case c == '}':
				depth--
				if opened && depth == 0 {
					// A group closing on the same line that opened it, with
					// another brace still to come, was part of the header
					// (`) map[K]struct{} {`, `): { ok: boolean } {`).
					if i == openLine && strings.Contains(l[j+1:], "{") {
						opened = false
						continue
					}
					return i + 1
				}
			case c == ';' && !opened && depth == 0 && parens == 0 && headerBraces == 0:
				return i + 1 // declaration without a body
			}
		}
		// Only template literals and raw strings span lines.
		if quote != 0 && quote != '`' {
			quote = 0
		}
	}
	return 0
}

// isHeaderContinuation reports whether a line at the declaration's own
// indentation still belongs to its header: an Allman-style opening brace or
// a trailing clause. (A closing paren of a multi-line parameter list never
// gets here: the caller only checks when no paren is open, so a leading `)`
// closes an enclosing group such as Go's `type ( ... )` instead.)
func isHeaderContinuation(t string) bool {
	for _, p := range []string{"{", ".", ":", ",", "->", "=>", "&&", "||", "throws ", "where ", "extends ", "implements "} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// lastNonBlank returns the last non-blank line in [from, before), 1-based.
func lastNonBlank(lines []string, from, before int) int {
	end := from
	for i := from; i < before && i <= len(lines); i++ {
		if strings.TrimSpace(lines[i-1]) != "" {
			end = i
		}
	}
	return end
}

// isCharLiteral reports whether the quote at l[j] opens a character literal
// like 'a', '\n', or '\u{1F600}', as opposed to a Rust lifetime ('a).
func isCharLiteral(l string, j int) bool {
	rest := l[j+1:]
	if len(rest) >= 2 && rest[0] != '\\' && rest[1] == '\'' {
		return true
	}
	if strings.HasPrefix(rest, "\\") {
		end := strings.IndexByte(rest[1:], '\'')
		return end >= 0 && end <= 10
	}
	// Multi-byte UTF-8 character literal, e.g. 'é'.
	if len(rest) > 0 && rest[0] >= 0x80 {
		end := strings.IndexByte(rest, '\'')
		return end > 0 && end <= 4
	}
	return false
}

// fallbackExtent ends the symbol just before the next declaration (or at the
// end of the file), trimming trailing blank lines.
func fallbackExtent(lines []string, decl, nextDecl int) int {
	end := len(lines)
	if nextDecl > decl && nextDecl-1 < end {
		end = nextDecl - 1
	}
	for end > decl && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return end
}

// leadingContextStart walks up from the declaration over its doc comment,
// decorators, and annotations (a contiguous block with no blank line).
func leadingContextStart(lines []string, decl int, language string) int {
	start := decl
	for i := decl - 2; i >= 0 && decl-1-i <= maxLeadingContextLines; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || !isLeadingContextLine(t, language) {
			break
		}
		start = i + 1
	}
	return start
}

func isLeadingContextLine(t, language string) bool {
	switch {
	case strings.HasPrefix(t, "//"), strings.HasPrefix(t, "/*"), strings.HasPrefix(t, "*"):
		return true
	case strings.HasPrefix(t, "@"):
		return true // decorators and annotations
	case strings.HasPrefix(t, "#["):
		return language == "Rust"
	case strings.HasPrefix(t, "#"):
		return language == "Python"
	case strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]"):
		return language == "C#" // attributes
	}
	return false
}
