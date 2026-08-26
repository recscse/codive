// Package symbols provides source code symbol extraction and skeleton generation.
package symbols

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/recscse/devctx/internal/db"
)

// GenerateSkeleton returns a token-efficient structural outline of a source file with stripped function bodies.
func GenerateSkeleton(relPath string, language string, content []byte, syms []db.SymbolRecord) string {
	if len(syms) == 0 {
		var err error
		syms, err = ExtractSymbols(relPath, language, content)
		if err != nil || len(syms) == 0 {
			return string(content)
		}
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("// === File Skeleton: %s (%d lines, %s) ===\n", filepath.ToSlash(relPath), totalLines, language))

	// Collect header (imports and package declarations)
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	inHeader := true
	headerCount := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if inHeader && headerCount < 30 {
			if strings.HasPrefix(trimmed, "package ") ||
				strings.HasPrefix(trimmed, "import ") ||
				strings.HasPrefix(trimmed, "from ") ||
				strings.HasPrefix(trimmed, "use ") ||
				strings.HasPrefix(trimmed, "#include") {
				sb.WriteString(line + "\n")
				headerCount++
				continue
			}
			if trimmed == "" && headerCount > 0 {
				inHeader = false
				sb.WriteString("\n")
			}
		}
	}

	sb.WriteString("// --- Declared Types & Symbol Signatures ---\n\n")
	for _, sym := range syms {
		sig := strings.TrimSpace(sym.Signature)
		if sig == "" {
			sig = sym.Name
		}

		switch sym.Kind {
		case "struct", "interface", "type", "class", "enum", "record", "trait":
			sb.WriteString(fmt.Sprintf("%s /* L%d */\n\n", sig, sym.LineNumber))
		case "function", "method", "impl":
			sb.WriteString(fmt.Sprintf("%s { /* L%d */ }\n\n", sig, sym.LineNumber))
		default:
			sb.WriteString(fmt.Sprintf("[%s] %s /* L%d */\n\n", sym.Kind, sig, sym.LineNumber))
		}
	}

	sb.WriteString(fmt.Sprintf("// === End of Skeleton (%d symbols) ===", len(syms)))
	return strings.TrimSpace(sb.String())
}
