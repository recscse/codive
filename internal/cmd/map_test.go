package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/recscse/codive/internal/db"
)

func TestProportionalMapBudget(t *testing.T) {
	tempDir := t.TempDir()

	// Initialize local SQLite
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

	// Create 20 files in api/ (early in alphabet)
	var files []db.FileRecord
	for i := 1; i <= 20; i++ {
		p := filepath.Join("api", filepath.FromSlash(strings.Repeat("long_file_name_for_testing_", 2)+string(rune('a'+i))+".go"))
		files = append(files, db.FileRecord{
			Path:      p,
			Language:  "Go",
			SizeBytes: 1024,
		})
	}

	// Create 2 files in web/ (late in alphabet)
	files = append(files, db.FileRecord{
		Path:      filepath.Join("web", "app.go"),
		Language:  "Go",
		SizeBytes: 512,
	})
	files = append(files, db.FileRecord{
		Path:      filepath.Join("web", "routes.go"),
		Language:  "Go",
		SizeBytes: 512,
	})

	if err := db.SaveFiles(ctx, database, files); err != nil {
		t.Fatalf("failed to save test files: %v", err)
	}

	// Generate map with tight token budget (e.g. 100 tokens = ~400 chars)
	out, err := GenerateMap(tempDir, MapOptions{
		TokenBudget: 100,
	})
	if err != nil {
		t.Fatalf("GenerateMap failed: %v", err)
	}

	// Verify that both api and web directories are present in the map!
	if !strings.Contains(out, "## api") {
		t.Errorf("expected map to contain '## api', got:\n%s", out)
	}
	if !strings.Contains(out, "## web") {
		t.Errorf("expected map to contain '## web' despite late alphabet, got:\n%s", out)
	}
	if !strings.Contains(out, "app.go") {
		t.Errorf("expected web/app.go to be preserved in proportional budget, got:\n%s", out)
	}
}
