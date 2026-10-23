package symbols

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The brace heuristic (used for JS/TS, Java, C#, Rust, C/C++, ...) is
// checked against Go's real parser on Go source, whose syntax exercises the
// same cases: strings, runes, raw strings, comments, multi-line signatures,
// bodyless and grouped declarations, and braces inside headers. By default
// this runs over this repository; set CODIVE_EXTENT_DIFF_ROOT to a large
// tree (e.g. $(go env GOROOT)/src, where it agrees on 99.99% of ~105k
// declarations) for a broader check.
func TestBraceHeuristicMatchesGoParser(t *testing.T) {
	root := os.Getenv("CODIVE_EXTENT_DIFF_ROOT")
	if root == "" {
		root = filepath.Join("..", "..")
	}
	total, agree := 0, 0
	var mismatches []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == "testdata" || d.Name() == ".git" || d.Name() == ".codive") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		syms, _ := ExtractSymbols(p, "Go", src)
		exact := goExtents(src)
		lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
		for _, s := range syms {
			ext, ok := exact[s.LineNumber]
			if !ok {
				continue
			}
			total++
			if braceExtent(lines, s.LineNumber, "Go") == ext[1] {
				agree++
			} else if len(mismatches) < 10 {
				mismatches = append(mismatches, p+":"+s.Name)
			}
		}
		return nil
	})
	if total == 0 {
		t.Fatal("no Go declarations found")
	}
	rate := float64(agree) / float64(total)
	t.Logf("%d/%d declarations agree (%.3f%%)", agree, total, 100*rate)
	if rate < 0.995 {
		t.Errorf("brace heuristic agreement %.2f%% is below 99.5%%; mismatches: %v", 100*rate, mismatches)
	}
}
