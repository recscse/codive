package symbols

import (
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
