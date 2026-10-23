package symbols

import (
	"strings"
	"testing"

	"github.com/recscse/codive/internal/db"
)

// extentOf extracts symbols from src the same way the indexer does, then
// returns the extent of the named symbol as "start-end".
func extentOf(t *testing.T, path, lang, src, name string) (int, int) {
	t.Helper()
	syms, err := ExtractSymbols(path, lang, []byte(src))
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	for i, s := range syms {
		if s.Name != name {
			continue
		}
		next := 0
		for _, o := range syms[i+1:] {
			if o.LineNumber > s.LineNumber {
				next = o.LineNumber
				break
			}
		}
		start, end, _ := SymbolExtent(lang, []byte(src), s, next)
		return start, end
	}
	t.Fatalf("symbol %s not extracted from %s; got %+v", name, path, syms)
	return 0, 0
}

func TestSymbolExtent(t *testing.T) {
	tests := []struct {
		name, path, lang, symbol string
		src                      string
		start, end               int
	}{
		{
			name: "go func with doc comment and braces in strings", path: "a.go", lang: "Go", symbol: "Parse",
			src: `package a

// Parse reads the input.
// It handles "}" in strings.
func Parse(s string) string {
	x := "}"
	y := '}'
	return x + string(y)
}

func Next() {}
`,
			start: 3, end: 9,
		},
		{
			name: "go method", path: "b.go", lang: "Go", symbol: "Run",
			src:   "package b\n\ntype S struct{}\n\n// Run runs.\nfunc (s *S) Run() {\n\tif true {\n\t}\n}\n",
			start: 5, end: 9,
		},
		{
			name: "go struct in grouped type block", path: "c.go", lang: "Go", symbol: "B",
			src:   "package c\n\ntype (\n\tA int\n\t// B is a pair.\n\tB struct {\n\t\tX, Y int\n\t}\n)\n",
			start: 5, end: 8,
		},
		{
			name: "python method with decorator and nested block", path: "m.py", lang: "Python", symbol: "handle",
			src: `class Svc:
    def other(self):
        pass

    @retry(3)
    def handle(self, req):
        if req:
            return 1

        # trailing comment inside body
        return 2

    def after(self):
        pass
`,
			start: 5, end: 11,
		},
		{
			name: "python class spans its methods", path: "k.py", lang: "Python", symbol: "Svc",
			src:   "class Svc:\n    def a(self):\n        pass\n\n    def b(self):\n        pass\n\nx = 1\n",
			start: 1, end: 6,
		},
		{
			name: "typescript class method with template literal", path: "s.ts", lang: "TypeScript", symbol: "render",
			src:   "export class View {\n  /** Renders. */\n  render(name: string): string {\n    const s = `}{${name}}`;\n    return s;\n  }\n\n  other() {}\n}\n",
			start: 2, end: 6,
		},
		{
			name: "java annotated method, braces in comment and char", path: "J.java", lang: "Java", symbol: "save",
			src: `public class Repo {
    /**
     * Saves it.
     */
    @Override
    @Transactional
    public void save(Item item) {
        char c = '{';
        // } not a real brace
        if (item != null) { store(item); }
    }

    public void other() {}
}
`,
			start: 2, end: 11,
		},
		{
			name: "java interface method without body", path: "I.java", lang: "Java", symbol: "find",
			src:   "public interface Repo {\n    Item find(long id);\n    void save(Item item);\n}\n",
			start: 2, end: 2,
		},
		{
			name: "rust fn with lifetimes and attribute", path: "l.rs", lang: "Rust", symbol: "longest",
			src:   "#[inline]\npub fn longest<'a>(x: &'a str, y: &'a str) -> &'a str {\n    if x.len() > y.len() { x } else { y }\n}\n\nfn next() {}\n",
			start: 1, end: 4,
		},
	}
	tests = append(tests, []struct {
		name, path, lang, symbol string
		src                      string
		start, end               int
	}{
		{
			name: "typescript type alias without body before a class", path: "t.ts", lang: "TypeScript", symbol: "Id",
			src:   "export type Id = string\nexport class Store {\n  get(id: Id) {}\n}\n",
			start: 1, end: 1,
		},
		{
			name: "react component with destructured props", path: "Card.tsx", lang: "TypeScript", symbol: "Card",
			src:   "export function Card({ title, body }: { title: string; body: string }) {\n  return <div>{title}</div>;\n}\n\nexport function Other() {}\n",
			start: 1, end: 3,
		},
		{
			name: "c# allman-style braces", path: "C.cs", lang: "C#", symbol: "Run",
			src:   "public class Job\n{\n    [Obsolete]\n    public void Run()\n    {\n        Work();\n    }\n}\n",
			start: 3, end: 7,
		},
	}...)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := extentOf(t, tt.path, tt.lang, tt.src, tt.symbol)
			if start != tt.start || end != tt.end {
				lines := strings.Split(tt.src, "\n")
				t.Errorf("extent = %d-%d, want %d-%d\n--- got:\n%s", start, end, tt.start, tt.end,
					strings.Join(lines[max(start-1, 0):min(end, len(lines))], "\n"))
			}
		})
	}
}

// A parameter list spanning several lines must not be mistaken for a
// bodyless declaration. (Tested directly: the TypeScript extractor doesn't
// yet index functions whose parameter list spans lines.)
func TestSymbolExtentMultiLineSignature(t *testing.T) {
	src := "export function build(\n  a: number,\n  b: number\n): number {\n  return a + b;\n}\n\nexport const x = 1;\n"
	start, end, _ := SymbolExtent("TypeScript", []byte(src), db.SymbolRecord{Name: "build", Kind: "function", LineNumber: 1}, 8)
	if start != 1 || end != 6 {
		t.Errorf("extent = %d-%d, want 1-6", start, end)
	}
}

func TestSymbolExtentFallback(t *testing.T) {
	src := "line1\nsym here\nbody\n\n\nnext sym\n"
	start, end, exact := SymbolExtent("Text", []byte(src), db.SymbolRecord{Name: "sym", LineNumber: 2}, 6)
	if start != 2 || end != 3 || exact {
		t.Errorf("fallback extent = %d-%d exact=%v, want 2-3 inexact", start, end, exact)
	}
}
