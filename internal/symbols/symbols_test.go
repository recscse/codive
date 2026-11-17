package symbols

import (
	"strings"
	"testing"
)

func TestExtractGoSymbols(t *testing.T) {
	goCode := `package main

type Config struct {
	Port int
}

type Service interface {
	Start() error
}

func NewService(cfg Config) *Server {
	return nil
}

func (s *Server) Start() error {
	return nil
}
`

	symbols, err := ExtractSymbols("server.go", "Go", []byte(goCode))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := map[string]string{
		"Config":     "struct",
		"Service":    "interface",
		"NewService": "function",
		"Start":      "method",
	}

	if len(symbols) != len(expected) {
		t.Fatalf("expected %d symbols, got %d", len(expected), len(symbols))
	}

	for _, s := range symbols {
		expectedKind, ok := expected[s.Name]
		if !ok {
			t.Errorf("unexpected symbol found: %s", s.Name)
			continue
		}
		if s.Kind != expectedKind {
			t.Errorf("symbol %s expected kind %s, got %s", s.Name, expectedKind, s.Kind)
		}
	}
}

func TestExtractPythonSymbols(t *testing.T) {
	pyCode := `@dataclass
class DatabaseClient:
    def __init__(self, url):
        self.url = url

    @property
    def connect(self):
        pass

@router.get("/stats")
async def calculate_stats(data):
    return len(data)
`

	symbols, err := ExtractSymbols("db.py", "Python", []byte(pyCode))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(symbols) < 3 {
		t.Fatalf("expected at least 3 symbols, got %d", len(symbols))
	}

	foundDecoratedClass := false
	foundDecoratedFunc := false
	for _, s := range symbols {
		if s.Name == "DatabaseClient" && s.Kind == "class" {
			foundDecoratedClass = true
		}
		if s.Name == "calculate_stats" && s.Kind == "function" {
			foundDecoratedFunc = true
		}
	}

	if !foundDecoratedClass {
		t.Errorf("failed to extract DatabaseClient class")
	}
	if !foundDecoratedFunc {
		t.Errorf("failed to extract calculate_stats function")
	}
}

func TestExtractTypeScriptSymbols(t *testing.T) {
	tsCode := `export interface User {
    id: string;
    name: string;
}

export type UserID = string;

export class UserService {
    getUser() {}
}

export function getUserById(id: string): User {
    return { id, name: "Alice" };
}

export const fetchUsers = async () => {
    return [];
};
`

	symbols, err := ExtractSymbols("user.ts", "TypeScript", []byte(tsCode))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// interface User, type UserID, class UserService, method getUser,
	// function getUserById, arrow function fetchUsers.
	if len(symbols) != 6 {
		t.Fatalf("expected 6 symbols, got %d: %+v", len(symbols), symbols)
	}

	want := map[string]string{
		"User":        "interface",
		"UserID":      "type",
		"UserService": "class",
		"getUser":     "method",
		"getUserById": "function",
		"fetchUsers":  "function",
	}
	got := make(map[string]string, len(symbols))
	for _, s := range symbols {
		got[s.Name] = s.Kind
	}
	for name, wantKind := range want {
		gotKind, ok := got[name]
		if !ok {
			t.Errorf("expected symbol %q not found", name)
			continue
		}
		if gotKind != wantKind {
			t.Errorf("symbol %q: expected kind %q, got %q", name, wantKind, gotKind)
		}
	}
}

func TestExtractRustSymbols(t *testing.T) {
	rustCode := `pub struct Config {
    pub port: u16,
}

pub trait Service {
    fn start(&self);
}

impl Service for Config {
    fn start(&self) {}
}

pub async fn run_server() {}
`

	symbols, err := ExtractSymbols("main.rs", "Rust", []byte(rustCode))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(symbols) < 4 {
		t.Fatalf("expected at least 4 symbols, got %d", len(symbols))
	}
}

// Go method signatures must keep their receiver: it's how agents (and
// read_symbol's Type.Method lookup) tell same-named methods apart.
func TestGoMethodSignatureKeepsReceiver(t *testing.T) {
	src := "package app\n\ntype Server struct{}\n\nfunc (s *Server) Start(port int) error { return nil }\n\nfunc (Server) Name() string { return \"\" }\n\nfunc (l *List[T]) Push(v T) {}\n"
	syms, err := ExtractSymbols("server.go", "Go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Start": "func (s *Server) Start(port int) error",
		"Name":  "func (Server) Name() string",
		"Push":  "func (l *List[T]) Push(v T)",
	}
	for _, s := range syms {
		if w, ok := want[s.Name]; ok && s.Signature != w {
			t.Errorf("%s signature = %q, want %q", s.Name, s.Signature, w)
		}
	}
}

// symbolsByName indexes extracted symbols by name for assertions.
func symbolsByName(t *testing.T, path, language, src string) map[string]int {
	t.Helper()
	syms, err := ExtractSymbols(path, language, []byte(src))
	if err != nil {
		t.Fatalf("ExtractSymbols(%s): %v", path, err)
	}
	out := make(map[string]int, len(syms))
	for _, s := range syms {
		out[s.Kind+" "+s.Name] = s.LineNumber
	}
	return out
}

// A declaration whose parameter list spans several lines must be indexed on
// the line its header starts on. Dropping it makes find_symbol answer "not
// found" for code that exists.
func TestMultiLineSignaturesAreIndexed(t *testing.T) {
	cases := []struct {
		name, path, language, src string
		want                      map[string]int
		absent                    []string
	}{
		{
			name: "python", path: "evaluate.py", language: "Python",
			src: `def one_line(a, b):
    return a + b


def multi_line(
    a,  # the (first) operand
    b,
):
    return a - b


async def multi_async(
    x: int,
) -> int:
    return x


class Report(
    Base,
    Mixin,
):
    @retry(
        times=3,
    )
    def render(
        self,
        title="(untitled)",
    ):
        return title


def last_one(z):
    return z
`,
			want: map[string]int{
				"function one_line": 1, "function multi_line": 5, "function multi_async": 12,
				"class Report": 18, "function render": 25, "function last_one": 32,
			},
		},
		{
			name: "typescript", path: "svc.ts", language: "TypeScript",
			src: `export function oneLine(a: number): number { return a; }

export function multiLine(
  a: number,
  b: number,
): number {
  return a + b;
}

export const multiArrow = async (
  a: number,
  b: number,
): Promise<number> => {
  return a + b;
};

const total = (
  [1, 2].map((n) => n * 2)
);

class Billing {
  async charge(
    amount: number,
    currency: string,
  ): Promise<void> {
    await this.send(amount);
  }

  refund(amount: number): void {}
}
`,
			want: map[string]int{
				"function oneLine": 1, "function multiLine": 3, "function multiArrow": 10,
				"class Billing": 21, "method charge": 22, "method refund": 29,
			},
			absent: []string{"function total"},
		},
		{
			name: "java", path: "Svc.java", language: "Java",
			src: `public class Svc {
    public int oneLine(int a) { return a; }

    @RequestMapping(
            value = "/multi",
            method = RequestMethod.GET)
    public int multiLine(
            int a,
            int b) {
        return compute(
                a,
                b);
    }

    private int compute(int a, int b) {
        if (a > b) return helper(a);
        else return (a + b);
    }

    void fail(String msg) {
        throw new CodingException(msg);
    }
}
`,
			want: map[string]int{
				"class Svc": 1, "method oneLine": 2, "method multiLine": 7,
				"method compute": 15, "method fail": 20,
			},
			absent: []string{"method helper", "method return", "method CodingException"},
		},
		{
			name: "rust", path: "lib.rs", language: "Rust",
			src: `pub fn one_line(a: i32) -> i32 { a }

pub async fn multi_line(
    a: i32,
    b: i32,
) -> i32 {
    a + b
}
`,
			want: map[string]int{"function one_line": 1, "function multi_line": 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := symbolsByName(t, tc.path, tc.language, tc.src)
			for sym, line := range tc.want {
				if gotLine, ok := got[sym]; !ok {
					t.Errorf("%s not indexed; got %v", sym, got)
				} else if gotLine != line {
					t.Errorf("%s indexed at L%d, want L%d", sym, gotLine, line)
				}
			}
			for _, sym := range tc.absent {
				if line, ok := got[sym]; ok {
					t.Errorf("%s indexed at L%d, but it is not a declaration", sym, line)
				}
			}
		})
	}
}

func TestMultiLineSignatureIsJoinedIntoOneLine(t *testing.T) {
	src := "def multi_line(\n    a,  # first\n    b=\"x)\",\n) -> int:\n    return a\n"
	syms, _ := ExtractSymbols("m.py", "Python", []byte(src))
	if len(syms) != 1 {
		t.Fatalf("got %d symbols, want 1: %+v", len(syms), syms)
	}
	if want := `def multi_line(a, b="x)") -> int:`; syms[0].Signature != want {
		t.Errorf("signature = %q, want %q", syms[0].Signature, want)
	}
}

// A line longer than bufio.Scanner's 64KB token limit used to end extraction
// for the rest of the file.
func TestDeclarationsAfterAVeryLongLineAreIndexed(t *testing.T) {
	src := "const blob = \"" + strings.Repeat("x", 70000) + "\";\nexport function after(a: number) { return a; }\n"
	got := symbolsByName(t, "big.ts", "TypeScript", src)
	if got["function after"] != 2 {
		t.Errorf("function after the long line not indexed; got %v", got)
	}
}
