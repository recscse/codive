// Package symbols provides source code symbol extraction and skeleton generation.
package symbols

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/recscse/codive/internal/db"
)

// GenerateSkeleton returns a token-efficient structural outline of a source file.
// It collapses function bodies to line-range markers so an AI agent can read the
// full API surface of a file in ~5% of the tokens needed to read the full file.
//
// Output format (Java example):
//
//	// File: src/main/java/...EventBridgeOperations.java (Total: 420 lines, Language: Java)
//	package com.arisglobal.scheduler.service;
//	@Service @Slf4j
//	public class EventBridgeOperations implements IEventBridgeOperations {
//	    // L45–L90: Constructor & bean initialization
//	    public EventBridgeOperations(EventBridgeClient client) { ... }
//	    // L95–L130: Rule creation & scheduling
//	    public PutRuleResponse createRule(CreateRuleRequest req) { ... }
//	}
func GenerateSkeleton(relPath string, language string, content []byte, syms []db.SymbolRecord) string {
	if len(syms) == 0 {
		var err error
		syms, err = ExtractSymbols(relPath, language, content)
		if err != nil || len(syms) == 0 {
			// No AST info — return a line-counted fallback
			lines := strings.Split(string(content), "\n")
			return fmt.Sprintf("// File: %s (Total: %d lines, Language: %s)\n// [No AST symbols — file content not available as skeleton]\n",
				filepath.ToSlash(relPath), len(lines), language)
		}
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)

	// Sort symbols by line number
	sort.Slice(syms, func(i, j int) bool {
		return syms[i].LineNumber < syms[j].LineNumber
	})

	// Compute end lines: symbol body ends just before the next symbol's declaration
	// (conservative estimate — real end-line needs a full parser)
	endLines := make([]int, len(syms))
	for i := range syms {
		if i+1 < len(syms) {
			next := syms[i+1].LineNumber - 1
			if next < syms[i].LineNumber {
				next = syms[i].LineNumber
			}
			endLines[i] = next
		} else {
			endLines[i] = totalLines
		}
	}

	var sb strings.Builder

	// ── File header ───────────────────────────────────────────────────────────
	sb.WriteString(fmt.Sprintf("// File: %s (Total: %d lines, Language: %s)\n",
		filepath.ToSlash(relPath), totalLines, language))
	sb.WriteString("\n")

	// ── Package / import header ───────────────────────────────────────────────
	headerLines := extractHeader(lines, language)
	if len(headerLines) > 0 {
		for _, l := range headerLines {
			sb.WriteString(l + "\n")
		}
		sb.WriteString("\n")
	}

	// ── Class / struct declaration wrapper ───────────────────────────────────
	// For OOP files, find the top-level class declaration and wrap output.
	classDecl := findClassDecl(syms)

	if classDecl != nil {
		sig := formatSig(classDecl.Signature)
		sb.WriteString(sig + " {\n")
	}

	indent := ""
	if classDecl != nil {
		indent = "    "
	}

	// ── Symbol entries ────────────────────────────────────────────────────────
	for i, sym := range syms {
		// Skip the class declaration itself — we already rendered it above
		if classDecl != nil && sym.Name == classDecl.Name && sym.Kind == classDecl.Kind {
			continue
		}

		start := sym.LineNumber
		end := endLines[i]

		// Compute a human-readable section label from the symbol
		label := sectionLabel(sym)

		switch sym.Kind {
		case "class", "struct", "interface", "enum", "record", "trait", "type":
			sig := formatSig(sym.Signature)
			sb.WriteString(fmt.Sprintf("%s// L%d: %s\n", indent, start, label))
			sb.WriteString(fmt.Sprintf("%s%s\n\n", indent, sig))

		case "function", "method", "impl", "constructor":
			sig := formatSig(sym.Signature)
			// Strip trailing { if the signature already has one
			sig = strings.TrimRight(strings.TrimRight(sig, " "), "{")
			sig = strings.TrimRight(sig, " ")
			sb.WriteString(fmt.Sprintf("%s// L%d–L%d: %s\n", indent, start, end, label))
			sb.WriteString(fmt.Sprintf("%s%s { ... }\n\n", indent, sig))

		case "annotation":
			// Spring/Java annotations: show on one line without body
			sig := formatSig(sym.Signature)
			sb.WriteString(fmt.Sprintf("%s%s  // L%d\n", indent, sig, start))

		case "dependency":
			sig := formatSig(sym.Signature)
			sb.WriteString(fmt.Sprintf("%s// [dependency] %s  // L%d\n", indent, sig, start))

		default:
			sig := formatSig(sym.Signature)
			sb.WriteString(fmt.Sprintf("%s// [%s] %s  // L%d\n", indent, sym.Kind, sig, start))
		}
	}

	if classDecl != nil {
		sb.WriteString("}\n")
	}

	sb.WriteString(fmt.Sprintf("\n// (%d symbols indexed)\n", len(syms)))
	return strings.TrimRight(sb.String(), "\n")
}

// extractHeader returns the first meaningful header lines (package, import, use, #include, from).
func extractHeader(lines []string, language string) []string {
	var header []string
	inImportBlock := false
	blankAfterImport := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if inImportBlock {
				blankAfterImport++
				if blankAfterImport > 2 {
					break
				}
			}
			if len(header) > 0 {
				header = append(header, "")
			}
			continue
		}
		// Stop at annotations and class/function declarations
		if strings.HasPrefix(trimmed, "@") && language == "Java" {
			break
		}
		if isCodeDeclaration(trimmed, language) {
			break
		}
		if strings.HasPrefix(trimmed, "package ") ||
			strings.HasPrefix(trimmed, "import ") ||
			strings.HasPrefix(trimmed, "from ") ||
			strings.HasPrefix(trimmed, "use ") ||
			strings.HasPrefix(trimmed, "#include") ||
			strings.HasPrefix(trimmed, "using ") {
			header = append(header, line)
			inImportBlock = true
			blankAfterImport = 0
		}
	}

	// Trim trailing blanks
	for len(header) > 0 && strings.TrimSpace(header[len(header)-1]) == "" {
		header = header[:len(header)-1]
	}
	return header
}

// isCodeDeclaration returns true if a line looks like a type or function declaration.
func isCodeDeclaration(line, language string) bool {
	_ = language
	for _, kw := range []string{"class ", "interface ", "struct ", "func ", "def ", "pub fn ", "fn ", "public class ", "private class "} {
		if strings.Contains(line, kw) {
			return true
		}
	}
	return false
}

// findClassDecl finds the primary class/struct/interface declaration in a symbol list.
func findClassDecl(syms []db.SymbolRecord) *db.SymbolRecord {
	for i := range syms {
		k := syms[i].Kind
		if k == "class" || k == "struct" || k == "interface" || k == "record" {
			return &syms[i]
		}
	}
	return nil
}

// sectionLabel returns a human-readable description for a symbol's collapsed section.
func sectionLabel(sym db.SymbolRecord) string {
	name := sym.Name
	// Heuristics for common patterns
	switch {
	case strings.EqualFold(name, "constructor") || strings.HasSuffix(name, "Constructor"):
		return "Constructor & initialization"
	case strings.HasPrefix(strings.ToLower(name), "get"):
		return fmt.Sprintf("Getter: %s", name)
	case strings.HasPrefix(strings.ToLower(name), "set"):
		return fmt.Sprintf("Setter: %s", name)
	case strings.HasPrefix(strings.ToLower(name), "create") || strings.HasPrefix(strings.ToLower(name), "save"):
		return fmt.Sprintf("Create / persist: %s", name)
	case strings.HasPrefix(strings.ToLower(name), "delete") || strings.HasPrefix(strings.ToLower(name), "remove"):
		return fmt.Sprintf("Delete / cleanup: %s", name)
	case strings.HasPrefix(strings.ToLower(name), "find") || strings.HasPrefix(strings.ToLower(name), "fetch") || strings.HasPrefix(strings.ToLower(name), "query"):
		return fmt.Sprintf("Query / fetch: %s", name)
	case strings.HasPrefix(strings.ToLower(name), "update") || strings.HasPrefix(strings.ToLower(name), "modify"):
		return fmt.Sprintf("Update: %s", name)
	case strings.HasPrefix(strings.ToLower(name), "handle") || strings.HasPrefix(strings.ToLower(name), "process"):
		return fmt.Sprintf("Handler: %s", name)
	case strings.HasPrefix(strings.ToLower(name), "validate") || strings.HasPrefix(strings.ToLower(name), "check"):
		return fmt.Sprintf("Validation: %s", name)
	case strings.HasPrefix(strings.ToLower(name), "send") || strings.HasPrefix(strings.ToLower(name), "publish") || strings.HasPrefix(strings.ToLower(name), "emit"):
		return fmt.Sprintf("Event / messaging: %s", name)
	case strings.HasPrefix(strings.ToLower(name), "test") || strings.HasSuffix(strings.ToLower(name), "test"):
		return fmt.Sprintf("Test: %s", name)
	default:
		return name
	}
}

// formatSig cleans up a signature for display: removes extra whitespace.
func formatSig(sig string) string {
	sig = strings.TrimSpace(sig)
	// Collapse internal multi-spaces
	for strings.Contains(sig, "  ") {
		sig = strings.ReplaceAll(sig, "  ", " ")
	}
	return sig
}
