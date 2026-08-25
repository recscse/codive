// Package symbols provides source code symbol extraction across supported languages.
package symbols

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"regexp"
	"strings"

	"github.com/recscse/ctxd/internal/db"
)

// ExtractSymbols parses source code based on its language and returns declared symbols.
func ExtractSymbols(relPath string, language string, content []byte) ([]db.SymbolRecord, error) {
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
				var buf bytes.Buffer
				printer.Fprint(&buf, fset, d.Recv)
				recvStr = buf.String() + " "
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
	jstsArrowFuncRegex = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z0-9_]+)\s*(?::\s*[^=]+)?\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z0-9_]+)\s*=>`)

	oopClassRegex     = regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|abstract|final|sealed|\s)*\s*class\s+([A-Za-z0-9_]+)`)
	oopInterfaceRegex = regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|abstract|\s)*\s*interface\s+([A-Za-z0-9_]+)`)
	oopRecordRegex    = regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|\s)*\s*record\s+([A-Za-z0-9_]+)`)
	oopEnumRegex      = regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|\s)*\s*enum\s+([A-Za-z0-9_]+)`)
	oopMethodRegex    = regexp.MustCompile(`^\s*(?:public|protected|private|static|async|virtual|override|abstract|final|\s)+\s+([A-Za-z0-9_<>[\],\s]+)\s+([A-Za-z0-9_]+)\s*\((.*?)\)`)

	rustFnRegex     = regexp.MustCompile(`^\s*(?:pub(?:\(.*?\))?\s+)?(?:async\s+)?fn\s+([A-Za-z0-9_]+)\s*(?:<.*?>)?\s*(\(.*?\))`)
	rustStructRegex = regexp.MustCompile(`^\s*(?:pub(?:\(.*?\))?\s+)?struct\s+([A-Za-z0-9_]+)`)
	rustEnumRegex   = regexp.MustCompile(`^\s*(?:pub(?:\(.*?\))?\s+)?enum\s+([A-Za-z0-9_]+)`)
	rustTraitRegex  = regexp.MustCompile(`^\s*(?:pub(?:\(.*?\))?\s+)?trait\s+([A-Za-z0-9_]+)`)
	rustImplRegex   = regexp.MustCompile(`^\s*impl(?:<.*?>)?\s+(?:([A-Za-z0-9_:]+)\s+for\s+)?([A-Za-z0-9_:]+)`)
)

func extractPythonSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	var currentDecorators []string

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if pyDecorRegex.MatchString(trimmed) {
			currentDecorators = append(currentDecorators, trimmed)
			continue
		}

		if m := pyClassRegex.FindStringSubmatch(line); len(m) > 1 {
			sig := trimmed
			if len(currentDecorators) > 0 {
				sig = strings.Join(currentDecorators, " ") + " " + sig
			}
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "class",
				Signature:  sig,
				LineNumber: lineNum,
			})
			currentDecorators = nil
			continue
		}

		if m := pyFuncRegex.FindStringSubmatch(line); len(m) > 1 {
			kind := "function"
			if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
				kind = "method"
			}
			sig := trimmed
			if len(currentDecorators) > 0 {
				sig = strings.Join(currentDecorators, " ") + " " + sig
			}
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       kind,
				Signature:  sig,
				LineNumber: lineNum,
			})
			currentDecorators = nil
			continue
		}

		currentDecorators = nil
	}
	return symbols, scanner.Err()
}

func extractJSTSSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if m := jstsClassRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "class",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := jstsInterfaceRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "interface",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := jstsTypeRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "type",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := jstsEnumRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "enum",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := jstsFuncRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "function",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := jstsArrowFuncRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "function",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
	}
	return symbols, scanner.Err()
}

func extractOOPCLikeSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if m := oopClassRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "class",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := oopInterfaceRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "interface",
				Signature:  trimmed,
				LineNumber: lineNum,
			})
			continue
		}
		if m := oopRecordRegex.FindStringSubmatch(line); len(m) > 1 {
			symbols = append(symbols, db.SymbolRecord{
				FilePath:   relPath,
				Name:       m[1],
				Kind:       "record",
				Signature:  trimmed,
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
			continue
		}
		if m := oopMethodRegex.FindStringSubmatch(line); len(m) > 2 {
			methodName := m[2]
			if methodName != "if" && methodName != "for" && methodName != "while" && methodName != "switch" {
				symbols = append(symbols, db.SymbolRecord{
					FilePath:   relPath,
					Name:       methodName,
					Kind:       "method",
					Signature:  trimmed,
					LineNumber: lineNum,
				})
			}
			continue
		}
	}
	return symbols, scanner.Err()
}

func extractRustSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	var symbols []db.SymbolRecord
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
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
	return symbols, scanner.Err()
}

func extractGenericSymbols(relPath string, content []byte) ([]db.SymbolRecord, error) {
	return nil, nil
}
