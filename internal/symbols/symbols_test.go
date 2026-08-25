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

	if len(symbols) != 5 {
		t.Fatalf("expected 5 symbols, got %d", len(symbols))
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
