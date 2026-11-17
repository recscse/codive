// Package symbols provides source code symbol extraction across supported languages.
package symbols

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/recscse/codive/internal/db"
)

// ExtractorVersion identifies what ExtractSymbols produces. Bump it whenever
// extraction output changes (a new language, a fixed signature, a new
// symbol kind): indexes built with a different version are rebuilt once in
// the background, since incremental sync only re-parses files that changed.
//
//	1: initial
//	2: Go method signatures include their receiver
//	3: definitions are indexed for search by the words they contain
//	4: declarations whose parameter list spans several lines are indexed
//	   (Python, JS/TS, Java, C#, Rust); Java/C# statements such as
//	   `return foo(x)` are no longer indexed as methods
const ExtractorVersion = "4"

// ExtractSymbols parses source code based on its language and returns declared symbols.
func ExtractSymbols(relPath string, language string, content []byte) ([]db.SymbolRecord, error) {
	base := strings.ToLower(filepath.Base(relPath))
	if base == "pom.xml" || strings.HasSuffix(base, "pom.xml") {
		return extractPomXMLSymbols(relPath, content)
	}
	if base == "build.gradle" || base == "build.gradle.kts" {
		return extractGradleSymbols(relPath, content)
	}
	if base == "package.json" {
		return extractPackageJSONSymbols(relPath, content)
	}

	switch language {
	case "Go":
		return extractGoSymbols(relPath, content)
	case "Python":
		return extractPythonSymbols(relPath, content)
	case "TypeScript", "JavaScript":
		return extractJSTSSymbols(relPath, content)
	case "Java", "C#":
		return extractOOPCLikeSymbols(relPath, content)
	case "Rust":
		return extractRustSymbols(relPath, content)
	default:
		return extractGenericSymbols(relPath, content)
	}
}

// extractGoSymbols extracts functions, methods, structs, interfaces, and types using Go's AST parser.
func extractGoSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, relPath, content, parser.ParseComments)
	if err != nil {
		return extractGenericSymbols(relPath, content)
	}

	var symbols []db.SymbolRecord

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			line := fset.Position(d.Pos()).Line
			kind := "function"
			var recvStr string
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = "method"
				// Print the receiver's type expression and name ourselves:
				// go/printer renders a bare *ast.FieldList as nothing, which
				// left every method signature as "func  Name(...)" with no
				// receiver at all.
				field := d.Recv.List[0]
				var buf bytes.Buffer
				printer.Fprint(&buf, fset, field.Type)
				if len(field.Names) > 0 {
					recvStr = "(" + field.Names[0].Name + " " + buf.String() + ") "
				} else {
					recvStr = "(" + buf.String() + ") "
				}
			}

			var sigBuf bytes.Buffer
			printer.Fprint(&sigBuf, fset, d.Type)
			fullSig := fmt.Sprintf("func %s%s%s", recvStr, d.Name.Name, strings.TrimPrefix(sigBuf.String(), "func"))

			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       d.Name.Name,
				Kind:       kind,
				Signature:  fullSig,
				LineNumber: line,
			})

		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok {
						line := fset.Position(typeSpec.Pos()).Line
						kind := "type"
						switch typeSpec.Type.(type) {
						case *ast.StructType:
							kind = "struct"
						case *ast.InterfaceType:
							kind = "interface"
						}

						symbols = append(symbols, db.SymbolRecord{
							FilePath:   relPath,
							Name:       typeSpec.Name.Name,
							Kind:       kind,
							Signature:  fmt.Sprintf("type %s %s", typeSpec.Name.Name, kind),
							LineNumber: line,
						})
					}
				}
			}
		}
	}

	return symbols, nil
}

// Regex patterns for various languages
var (
	pyClassRegex = regexp.MustCompile(`^\s*class\s+([A-Za-z0-9_]+)(?:\((.*?)\))?:`)
	pyFuncRegex  = regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z0-9_]+)\s*\((.*?)\)`)
	pyDecorRegex = regexp.MustCompile(`^\s*@([A-Za-z0-9_.]+)(?:\(.*?\))?`)

	jstsClassRegex     = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?class\s+([A-Za-z0-9_]+)`)
	jstsInterfaceRegex = regexp.MustCompile(`^\s*(?:export\s+)?interface\s+([A-Za-z0-9_]+)`)
	jstsTypeRegex      = regexp.MustCompile(`^\s*(?:export\s+)?type\s+([A-Za-z0-9_]+)`)
	jstsEnumRegex      = regexp.MustCompile(`^\s*(?:export\s+)?enum\s+([A-Za-z0-9_]+)`)
	jstsFuncRegex      = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z0-9_]+)\s*(?:<.*?>)?\s*(\(.*?\))`)
	jstsArrowFuncRegex = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z0-9_]+)\s*(?::\s*[^=]+)?\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z0-9_]+)\s*(?::\s*[^=]+)?\s*=>`)

	// jstsMethodRegex matches an ES6 class method or constructor declaration
	// (no `function` keyword, e.g. `async processPayment(x): Promise<T> {`).
	// Only applied while inside a class body (see classBodyDepth in
	// extractJSTSSymbols) so it never matches control-flow blocks like
	// `if (x) {` at the top level.
	jstsMethodRegex = regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|static\s+|readonly\s+|abstract\s+|async\s+|get\s+|set\s+)*([A-Za-z_][A-Za-z0-9_]*)\s*(?:<[^>]*>)?\s*\(([^)]*)\)\s*(?::\s*[^{;]+)?\s*\{`)

	// Java, Spring Boot & C# regexes
	oopAnnotationRegex = regexp.MustCompile(`^\s*@([A-Za-z0-9_]+)(?:\((.*?)\))?`)
	oopClassRegex      = regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|abstract|final|sealed|\s)*\s*class\s+([A-Za-z0-9_]+)`)
	oopInterfaceRegex  = regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|abstract|\s)*\s*interface\s+([A-Za-z0-9_]+)`)
	oopRecordRegex     = regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|\s)*\s*record\s+([A-Za-z0-9_]+)`)
	oopEnumRegex       = regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|\s)*\s*enum\s+([A-Za-z0-9_]+)`)
	oopMethodRegex     = regexp.MustCompile(`^\s*(?:public|protected|private|static|async|virtual|override|abstract|final|\s)+\s+([A-Za-z0-9_<>[\],\s]+)\s+([A-Za-z0-9_]+)\s*\((.*?)\)`)

	rustFnRegex     = regexp.MustCompile(`^\s*(?:pub(?:\(.*?\))?\s+)?(?:async\s+)?fn\s+([A-Za-z0-9_]+)\s*(?:<.*?>)?\s*(\(.*?\))`)
	rustStructRegex = regexp.MustCompile(`^\s*(?:pub(?:\(.*?\))?\s+)?struct\s+([A-Za-z0-9_]+)`)
	rustEnumRegex   = regexp.MustCompile(`^\s*(?:pub(?:\(.*?\))?\s+)?enum\s+([A-Za-z0-9_]+)`)
	rustTraitRegex  = regexp.MustCompile(`^\s*(?:pub(?:\(.*?\))?\s+)?trait\s+([A-Za-z0-9_]+)`)
	rustImplRegex   = regexp.MustCompile(`^\s*impl(?:<.*?>)?\s+(?:([A-Za-z0-9_:]+)\s+for\s+)?([A-Za-z0-9_:]+)`)

	// The *StartRegex patterns match the first line of a declaration whose
	// parameter list may continue on later lines (`def name(` + newline). They
	// only decide whether joining the following lines is worth trying; the
	// joined text must still match the full pattern above.
	pyDeclStartRegex     = regexp.MustCompile(`^\s*(?:(?:async\s+)?def|class)\s+[A-Za-z0-9_]+\s*\(`)
	jstsFuncStartRegex   = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\b`)
	jstsArrowStartRegex  = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+[A-Za-z0-9_]+\s*(?::\s*[^=]+)?\s*=\s*(?:async\s*)?\(`)
	jstsMethodStartRegex = regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|static\s+|readonly\s+|abstract\s+|async\s+|get\s+|set\s+)*[A-Za-z_][A-Za-z0-9_]*\s*(?:<[^>]*>)?\s*\(`)
	jstsArrowTailRegex   = regexp.MustCompile(`^\s*(?::\s*[^=]+)?\s*=>`)
	oopMethodStartRegex  = regexp.MustCompile(`^\s*(?:public|protected|private|static|async|virtual|override|abstract|final|\s)+\s+[A-Za-z0-9_<>[\],\s]+\s+[A-Za-z0-9_]+\s*\(`)
	rustFnStartRegex     = regexp.MustCompile(`^\s*(?:pub(?:\(.*?\))?\s+)?(?:async\s+)?fn\s+[A-Za-z0-9_]+`)
)

const (
	// maxSignatureLines bounds how many lines a declaration's header is
	// followed over while looking for the parenthesis that closes it.
	maxSignatureLines = 40
	// maxSignatureChars caps a signature assembled from several lines.
	maxSignatureChars = 600
	// maxDeclLineChars is the longest line searched for a declaration. Longer
	// lines are minified or generated code: their "signature" would be the
	// whole line.
	maxDeclLineChars = 4000
)

// sourceLines splits content into lines, without their line endings.
func sourceLines(content []byte) []string {
	lines := strings.Split(string(content), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// lexRules tells code from string literals and line comments in a language,
// closely enough to count parentheses on a line.
type lexRules struct {
	lineComment string
	quotes      string // bytes that open and close a string literal
}

var (
	pyLex    = lexRules{lineComment: "#", quotes: `"'`}
	cLikeLex = lexRules{lineComment: "//", quotes: "\"'`"} // JS/TS, Java, C#
	rustLex  = lexRules{lineComment: "//", quotes: `"`}    // ' also starts a lifetime
)

// scan returns line without its trailing line comment and the net number of
// parentheses its code opens (negative when it closes more than it opens).
func (r lexRules) scan(line string) (code string, parens int) {
	var quote byte
	for j := 0; j < len(line); j++ {
		c := line[j]
		switch {
		case quote != 0:
			if c == '\\' {
				j++
			} else if c == quote {
				quote = 0
			}
		case strings.IndexByte(r.quotes, c) >= 0:
			quote = c
		case strings.HasPrefix(line[j:], r.lineComment):
			return strings.TrimRight(line[:j], " \t"), parens
		case c == '(':
			parens++
		case c == ')':
			parens--
		}
	}
	return line, parens
}

// joinDeclaration returns the declaration starting on lines[i] as one line.
// When its parameter list is still open at the end of lines[i], the lines
// after it are appended (without their comments) until the parentheses
// balance. n is the number of source lines used: 1 means lines[i] is returned
// unchanged, either because it closes what it opens or because no closing
// parenthesis was found within maxSignatureLines.
func (r lexRules) joinDeclaration(lines []string, i int) (joined string, n int) {
	code, depth := r.scan(lines[i])
	if depth <= 0 {
		return lines[i], 1
	}
	var sb strings.Builder
	sb.WriteString(code)
	for j := i + 1; j < len(lines) && j-i < maxSignatureLines; j++ {
		if len(lines[j]) > maxDeclLineChars {
			break
		}
		code, d := r.scan(lines[j])
		if t := strings.TrimSpace(code); t != "" {
			sb.WriteByte(' ')
			sb.WriteString(t)
		}
		depth += d
		if depth <= 0 {
			return sb.String(), j - i + 1
		}
	}
	return lines[i], 1
}

// tidySignature renders a declaration joined from several lines as one
// readable line: `def f( a, b, ):` becomes `def f(a, b):`.
func tidySignature(joined string) string {
	s := strings.Join(strings.Fields(joined), " ")
	s = strings.ReplaceAll(s, "( ", "(")
	s = strings.ReplaceAll(s, " )", ")")
	s = strings.ReplaceAll(s, ",)", ")")
	if len(s) > maxSignatureChars {
		cut := maxSignatureChars
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

func extractPythonSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	lines := sourceLines(content)
	var currentDecorators []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		lineNum := i + 1
		if len(line) > maxDeclLineChars {
			currentDecorators = nil
			continue
		}
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// A def or class header, or a decorator call, may span several lines.
		// The symbol is still reported on the line its header starts on.
		if pyDeclStartRegex.MatchString(line) || pyDecorRegex.MatchString(trimmed) {
			if joined, n := pyLex.joinDeclaration(lines, i); n > 1 {
				line, trimmed = joined, tidySignature(joined)
				i += n - 1
			}
		}

		if pyDecorRegex.MatchString(trimmed) {
			currentDecorators = append(currentDecorators, trimmed)
			continue
		}

		if m := pyClassRegex.FindStringSubmatch(line); len(m) > 1 {
			sig := trimmed
			if len(currentDecorators) > 0 {
				sig = strings.Join(currentDecorators, " ") + " " + trimmed
				currentDecorators = nil
			}
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "class",
				Signature:  sig,
				LineNumber: lineNum,
			})
			continue
		}

		if m := pyFuncRegex.FindStringSubmatch(line); len(m) > 1 {
			sig := trimmed
			if len(currentDecorators) > 0 {
				sig = strings.Join(currentDecorators, " ") + " " + trimmed
				currentDecorators = nil
			}
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "function",
				Signature:  sig,
				LineNumber: lineNum,
			})
			continue
		}

		currentDecorators = nil
	}
	return symbols, nil
}

// jsControlKeywords excludes control-flow constructs that could otherwise be
// misread as a class method by jstsMethodRegex's name capture group (e.g.
// `if (x) {`, `for (;;) {`). classBodyDepth gating already excludes nearly
// all of these on its own since control-flow blocks sit one level deeper
// than direct class members, but this is a cheap second line of defense.
var jsControlKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"function": true, "return": true, "else": true, "do": true, "try": true,
}

func extractJSTSSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	lines := sourceLines(content)

	// braceDepth is a running per-line `{`/`}` count, not a real parse — it's
	// only precise enough to answer "is this line directly inside the most
	// recently opened class body," which is what lets us detect ES6 class
	// methods (declared with no `function` keyword) without also matching
	// control-flow blocks anywhere else in the file.
	braceDepth := 0
	classBodyDepth := -1

	// match records the declaration on line, if there is one. multiLine marks
	// a header joined from several source lines, which gets a stricter arrow
	// function check: `const x = (` followed by lines containing some `=>` is
	// only a function when the arrow comes right after its parameter list.
	match := func(line, sig string, lineNum int, multiLine bool) bool {
		add := func(name, kind string) bool {
			symbols = append(symbols, db.SymbolRecord{FilePath: relPath, Name: name, Kind: kind, Signature: sig, LineNumber: lineNum})
			return true
		}
		if m := jstsClassRegex.FindStringSubmatch(line); len(m) > 1 {
			classBodyDepth = braceDepth + 1
			return add(m[1], "class")
		}
		if m := jstsInterfaceRegex.FindStringSubmatch(line); len(m) > 1 {
			return add(m[1], "interface")
		}
		if m := jstsTypeRegex.FindStringSubmatch(line); len(m) > 1 {
			return add(m[1], "type")
		}
		if m := jstsEnumRegex.FindStringSubmatch(line); len(m) > 1 {
			return add(m[1], "enum")
		}
		if m := jstsFuncRegex.FindStringSubmatch(line); len(m) > 1 {
			return add(m[1], "function")
		}
		if m := jstsArrowFuncRegex.FindStringSubmatch(line); len(m) > 1 && (!multiLine || arrowFollowsParams(line)) {
			return add(m[1], "function")
		}
		if classBodyDepth >= 0 && braceDepth == classBodyDepth {
			if m := jstsMethodRegex.FindStringSubmatch(line); len(m) > 1 && !jsControlKeywords[m[1]] {
				return add(m[1], "method")
			}
		}
		return false
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		consumed := 1

		isCommentOrBlank := trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")

		if !isCommentOrBlank && len(line) <= maxDeclLineChars {
			matched := false
			inClassBody := classBodyDepth >= 0 && braceDepth == classBodyDepth
			if jstsFuncStartRegex.MatchString(line) || jstsArrowStartRegex.MatchString(line) ||
				(inClassBody && jstsMethodStartRegex.MatchString(line)) {
				// The parameter list may continue on the following lines. If
				// the joined header isn't a declaration after all, those
				// lines are scanned on their own as usual.
				if joined, n := cLikeLex.joinDeclaration(lines, i); n > 1 && match(joined, tidySignature(joined), i+1, true) {
					matched, consumed = true, n
				}
			}
			if !matched {
				match(line, trimmed, i+1, false)
			}
		}

		for _, l := range lines[i : i+consumed] {
			braceDepth += strings.Count(l, "{") - strings.Count(l, "}")
		}
		if classBodyDepth >= 0 && braceDepth < classBodyDepth {
			classBodyDepth = -1
		}
		i += consumed - 1
	}
	return symbols, nil
}

// arrowFollowsParams reports whether, in a `const name = (...` declaration
// joined from several lines, the parenthesis opened after `=` closes right
// before `=>` (optionally with a return type in between). That separates an
// arrow function from a parenthesized expression that merely contains one.
func arrowFollowsParams(joined string) bool {
	loc := jstsArrowStartRegex.FindStringIndex(joined)
	if loc == nil {
		return false
	}
	depth := 1
	var quote byte
	for j := loc[1]; j < len(joined); j++ {
		c := joined[j]
		switch {
		case quote != 0:
			if c == '\\' {
				j++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return jstsArrowTailRegex.MatchString(joined[j+1:])
			}
		}
	}
	return false
}

// oopStatementKeywords can't appear in a method declaration's return type.
// oopMethodRegex only requires `type name(`, which statements such as
// `return compute(x)` and `throw new MyError(msg)` also fit.
var oopStatementKeywords = map[string]bool{
	"return": true, "throw": true, "else": true, "case": true, "yield": true,
	"await": true, "assert": true, "goto": true, "do": true, "in": true,
	"instanceof": true, "typeof": true, "break": true, "continue": true,
}

// oopNotMethodNames are keywords followed by `(` that aren't method names.
var oopNotMethodNames = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"return": true, "super": true, "this": true, "using": true, "lock": true,
	"foreach": true, "fixed": true, "when": true,
}

// oopMethodName returns the name of the Java/C# method declared on line.
func oopMethodName(line string) (string, bool) {
	m := oopMethodRegex.FindStringSubmatch(line)
	if len(m) <= 2 || oopNotMethodNames[m[2]] {
		return "", false
	}
	typeWords := strings.Fields(m[1])
	for _, w := range typeWords {
		if oopStatementKeywords[w] {
			return "", false
		}
	}
	// `new Thread(r)` is a constructor call. (`new void Hide()`, with C#'s
	// `new` modifier before the type, still is a declaration.)
	if n := len(typeWords); n > 0 && typeWords[n-1] == "new" {
		return "", false
	}
	return m[2], true
}

func extractOOPCLikeSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	lines := sourceLines(content)
	var currentAnnotations []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		lineNum := i + 1
		if len(line) > maxDeclLineChars {
			currentAnnotations = nil
			continue
		}
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		// Collect annotations (Spring / Java / C#)
		if oopAnnotationRegex.MatchString(trimmed) {
			// An annotation's arguments may span several lines; keep it whole
			// so it stays attached to the declaration that follows.
			if joined, n := cLikeLex.joinDeclaration(lines, i); n > 1 {
				trimmed = tidySignature(joined)
				i += n - 1
			}
			currentAnnotations = append(currentAnnotations, trimmed)
			// Also register Spring framework annotations directly for instant discovery
			if m := oopAnnotationRegex.FindStringSubmatch(trimmed); len(m) > 1 {
				annName := m[1]
				if isSignificantAnnotation(annName) {
					symbols = append(symbols, db.SymbolRecord{
						FilePath:   relPath,
						Name:       "@" + annName,
						Kind:       "annotation",
						Signature:  trimmed,
						LineNumber: lineNum,
					})
				}
			}
			continue
		}

		if m := oopClassRegex.FindStringSubmatch(line); len(m) > 1 {
			sig := trimmed
			if len(currentAnnotations) > 0 {
				sig = strings.Join(currentAnnotations, " ") + " " + trimmed
				currentAnnotations = nil
			}
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "class",
				Signature:  sig,
				LineNumber: lineNum,
			})
			continue
		}
		if m := oopInterfaceRegex.FindStringSubmatch(line); len(m) > 1 {
			sig := trimmed
			if len(currentAnnotations) > 0 {
				sig = strings.Join(currentAnnotations, " ") + " " + trimmed
				currentAnnotations = nil
			}
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "interface",
				Signature:  sig,
				LineNumber: lineNum,
			})
			continue
		}
		if m := oopRecordRegex.FindStringSubmatch(line); len(m) > 1 {
			sig := trimmed
			if len(currentAnnotations) > 0 {
				sig = strings.Join(currentAnnotations, " ") + " " + trimmed
				currentAnnotations = nil
			}
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "record",
				Signature:  sig,
				LineNumber: lineNum,
			})
			continue
		}
		if m := oopEnumRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "enum",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			currentAnnotations = nil
			continue
		}
		methodName, isMethod := oopMethodName(line)
		if !isMethod && oopMethodStartRegex.MatchString(line) {
			// The parameter list may continue on the following lines.
			if joined, n := cLikeLex.joinDeclaration(lines, i); n > 1 {
				if methodName, isMethod = oopMethodName(joined); isMethod {
					trimmed = tidySignature(joined)
					i += n - 1
				}
			}
		}
		if isMethod {
			sig := trimmed
			if len(currentAnnotations) > 0 {
				sig = strings.Join(currentAnnotations, " ") + " " + trimmed
				currentAnnotations = nil
			}
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       methodName,
				Kind:       "method",
				Signature:  sig,
				LineNumber: lineNum,
			})
			continue
		}

		currentAnnotations = nil
	}
	return symbols, nil
}

func isSignificantAnnotation(name string) bool {
	switch name {
	case "RestController", "Controller", "Service", "Repository", "Component", "Configuration",
		"Bean", "Scheduled", "GetMapping", "PostMapping", "PutMapping", "DeleteMapping",
		"PatchMapping", "RequestMapping", "Autowired", "Value", "EventListener":
		return true
	default:
		return false
	}
}

func extractRustSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	lines := sourceLines(content)

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		lineNum := i + 1
		if len(line) > maxDeclLineChars {
			continue
		}
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		// A fn's parameter list may span several lines.
		if rustFnStartRegex.MatchString(line) {
			if joined, n := rustLex.joinDeclaration(lines, i); n > 1 {
				line, trimmed = joined, tidySignature(joined)
				i += n - 1
			}
		}

		if m := rustFnRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "function",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := rustStructRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "struct",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := rustTraitRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "trait",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := rustEnumRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "enum",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := rustImplRegex.FindStringSubmatch(line); len(m) > 2 {
			name := m[2]
			if m[1] != "" {
				name = fmt.Sprintf("%s for %s", m[1], m[2])
			}
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       name,
				Kind:       "impl",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
	}
	return symbols, nil
}

// Maven POM.xml XML structs
type pomXML struct {
	XMLName      xml.Name        `xml:"project"`
	GroupId      string          `xml:"groupId"`
	ArtifactId   string          `xml:"artifactId"`
	Version      string          `xml:"version"`
	Name         string          `xml:"name"`
	Parent       *pomParent      `xml:"parent"`
	Dependencies []pomDependency `xml:"dependencies>dependency"`
	Properties   []pomProperty   `xml:"properties"`
}

type pomParent struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
}

type pomDependency struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
}

type pomProperty struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

func extractPomXMLSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	var p pomXML
	if err := xml.Unmarshal(content, &p); err != nil {
		return extractGenericSymbols(relPath, content)
	}

	if p.ArtifactId != "" {
		symbols = append(symbols, db.SymbolRecord{
			FilePath:   relPath,
			Name:       p.ArtifactId,
			Kind:       "project",
			Signature:  fmt.Sprintf("Maven Artifact: %s:%s (version: %s)", p.GroupId, p.ArtifactId, p.Version),
			LineNumber: 1,
		})
	}

	if p.Parent != nil {
		symbols = append(symbols, db.SymbolRecord{
			FilePath:   relPath,
			Name:       p.Parent.ArtifactId,
			Kind:       "parent_pom",
			Signature:  fmt.Sprintf("Parent POM: %s:%s (version: %s)", p.Parent.GroupId, p.Parent.ArtifactId, p.Parent.Version),
			LineNumber: 1,
		})
	}

	for _, dep := range p.Dependencies {
		symbols = append(symbols, db.SymbolRecord{
			FilePath:   relPath,
			Name:       dep.ArtifactId,
			Kind:       "dependency",
			Signature:  fmt.Sprintf("Dependency: %s:%s (version: %s, scope: %s)", dep.GroupId, dep.ArtifactId, dep.Version, dep.Scope),
			LineNumber: 1,
		})
	}

	return symbols, nil
}

func extractGradleSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0

	depRegex := regexp.MustCompile(`(implementation|api|testImplementation|compileOnly|runtimeOnly)\s*[\('"]+([^'"\)]+)[\)'"]+`)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if m := depRegex.FindStringSubmatch(line); len(m) > 2 {
			depCoord := m[2]
			parts := strings.Split(depCoord, ":")
			depName := depCoord
			if len(parts) >= 2 {
				depName = parts[1]
			}
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       depName,
				Kind:       "dependency",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
		}
	}
	return symbols, nil
}

type packageJSON struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func extractPackageJSONSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	var pkg packageJSON
	if err := json.Unmarshal(content, &pkg); err != nil {
		return nil, nil
	}

	if pkg.Name != "" {
		symbols = append(symbols, db.SymbolRecord{
			FilePath:   relPath,
			Name:       pkg.Name,
			Kind:       "package",
			Signature:  fmt.Sprintf("npm Package: %s (version: %s)", pkg.Name, pkg.Version),
			LineNumber: 1,
		})
	}

	for dep, ver := range pkg.Dependencies {
		symbols = append(symbols, db.SymbolRecord{
			FilePath:   relPath,
			Name:       dep,
			Kind:       "dependency",
			Signature:  fmt.Sprintf("npm dep: %s@%s", dep, ver),
			LineNumber: 1,
		})
	}
	for dep, ver := range pkg.DevDependencies {
		symbols = append(symbols, db.SymbolRecord{
			FilePath:   relPath,
			Name:       dep,
			Kind:       "devDependency",
			Signature:  fmt.Sprintf("npm devDep: %s@%s", dep, ver),
			LineNumber: 1,
		})
	}

	return symbols, nil
}

func extractGenericSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	return nil, nil
}
