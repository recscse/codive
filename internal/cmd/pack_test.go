package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/recscse/codive/internal/db"
)

func TestPackFeatureContextAttentionOrder(t *testing.T) {
	tempDir := t.TempDir()

	codiveDir := filepath.Join(tempDir, ".codive")
	if err := os.MkdirAll(codiveDir, 0755); err != nil {
		t.Fatalf("failed to create .codive dir: %v", err)
	}

	database, err := db.Open(filepath.Join(codiveDir, "index.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer database.Close()

	if err := db.InitSchema(database); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	ctx := context.Background()

	// Insert symbols: A struct (Type), a function (Implementation), and a test
	syms := []db.SymbolRecord{
		{
			Name:       "SessionConfig",
			Kind:       "struct",
			Signature:  "type SessionConfig struct",
			FilePath:   "auth/session.go",
			LineNumber: 10,
		},
		{
			Name:       "VerifyCredentials",
			Kind:       "function",
			Signature:  "func VerifyCredentials(u, p string) bool",
			FilePath:   "auth/login.go",
			LineNumber: 42,
		},
	}
	if err := db.SaveSymbols(ctx, database, syms); err != nil {
		t.Fatalf("failed to save symbols: %v", err)
	}

	// Pack context for "login" (should expand to match VerifyCredentials and SessionConfig)
	out, err := PackFeatureContext(ctx, database, tempDir, "login")
	if err != nil {
		t.Fatalf("PackFeatureContext failed: %v", err)
	}

	// Verify that Core Types is placed before Implementation Functions in output (Attention U-Curve)
	typeIndex := strings.Index(out, "Core Types & Interfaces")
	fnIndex := strings.Index(out, "Implementation Functions")

	if typeIndex == -1 {
		t.Errorf("expected 'Core Types & Interfaces' in output, got:\n%s", out)
	}
	if fnIndex == -1 {
		t.Errorf("expected 'Implementation Functions' in output, got:\n%s", out)
	}
	if typeIndex >= fnIndex {
		t.Errorf("Attention U-Curve violated: Core Types (pos %d) should appear before Implementation Functions (pos %d)", typeIndex, fnIndex)
	}

	// Verify intent expansion matched VerifyCredentials
	if !strings.Contains(out, "VerifyCredentials") {
		t.Errorf("expected expanded intent to find 'VerifyCredentials', got:\n%s", out)
	}
}
