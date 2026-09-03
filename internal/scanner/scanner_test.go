package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/recscse/codive/internal/db"
)

func TestScanner(t *testing.T) {
	// 1. Create a temporary fixture directory
	tempDir, err := os.MkdirTemp("", "codive_scanner_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 2. Set up valid test files in different languages
	filesToCreate := map[string]string{
		"main.go":              "package main\n\nfunc main() {}\n",
		"script.py":            "print('hello world')\n",
		"app.ts":               "const greeting: string = 'hello';\n",
		"service.java":         "public class Service {}\n",
		"Program.cs":           "using System;\nclass Program {}\n",
		"nested/deep/util.py":  "def add(a, b): return a + b\n",
		"README.md":            "# Test Project\n",
		"empty.js":             "",
	}

	for relPath, content := range filesToCreate {
		fullPath := filepath.Join(tempDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create dirs for %s: %v", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", relPath, err)
		}
	}

	// 3. Create files in ignored directories (should all be skipped)
	ignoredFiles := []string{
		".git/config",
		"node_modules/express/index.js",
		"vendor/github.com/pkg/errors/errors.go",
		"build/output.js",
		"dist/bundle.js",
		"target/app.jar",
		"bin/binary.exe",
		"obj/debug.o",
		".codive/index.db",
	}

	for _, relPath := range ignoredFiles {
		fullPath := filepath.Join(tempDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create ignored dir for %s: %v", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte("ignored content"), 0644); err != nil {
			t.Fatalf("failed to write ignored file %s: %v", relPath, err)
		}
	}

	// 4. Create a binary file (should be skipped)
	binaryPath := filepath.Join(tempDir, "sample.dat")
	binaryContent := []byte{0x7F, 0x45, 0x4C, 0x46, 0x00, 0x01, 0x02, 0x03}
	if err := os.WriteFile(binaryPath, binaryContent, 0644); err != nil {
		t.Fatalf("failed to write binary file: %v", err)
	}

	// 5. Run Scan
	result, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// 6. Verify scanned files count
	if len(result.Files) != len(filesToCreate) {
		t.Errorf("expected %d scanned files, got %d", len(filesToCreate), len(result.Files))
		for _, f := range result.Files {
			t.Logf("scanned file: %s (%s)", f.Path, f.Language)
		}
	}

	// 7. Verify language detection and content hashing
	fileMap := make(map[string]string)
	hashMap := make(map[string]string)
	for _, f := range result.Files {
		fileMap[f.Path] = f.Language
		hashMap[f.Path] = f.ContentHash
	}

	expectedLanguages := map[string]string{
		"main.go":             "Go",
		"script.py":           "Python",
		"app.ts":              "TypeScript",
		"service.java":        "Java",
		"Program.cs":          "C#",
		"nested/deep/util.py": "Python",
		"README.md":           "Markdown",
		"empty.js":            "JavaScript",
	}

	for path, expectedLang := range expectedLanguages {
		actualLang, exists := fileMap[path]
		if !exists {
			t.Errorf("expected file %s to be indexed, but was not found", path)
			continue
		}
		if actualLang != expectedLang {
			t.Errorf("file %s expected language %s, got %s", path, expectedLang, actualLang)
		}

		// Verify SHA-256 hash
		content := filesToCreate[path]
		expectedHashBytes := sha256.Sum256([]byte(content))
		expectedHash := hex.EncodeToString(expectedHashBytes[:])
		if hashMap[path] != expectedHash {
			t.Errorf("file %s expected hash %s, got %s", path, expectedHash, hashMap[path])
		}
	}

	// 8. Verify language counts summary
	if result.LanguageCounts["Python"] != 2 {
		t.Errorf("expected 2 Python files, got %d", result.LanguageCounts["Python"])
	}
	if result.LanguageCounts["Go"] != 1 {
		t.Errorf("expected 1 Go file, got %d", result.LanguageCounts["Go"])
	}
}

func TestLargeFileExclusion(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codive_large_file_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a 6MB dummy file
	largeFile := filepath.Join(tempDir, "large.py")
	f, err := os.Create(largeFile)
	if err != nil {
		t.Fatalf("failed to create large file: %v", err)
	}
	if err := f.Truncate(6 * 1024 * 1024); err != nil {
		f.Close()
		t.Fatalf("failed to truncate large file: %v", err)
	}
	f.Close()

	result, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(result.Files) != 0 {
		t.Errorf("expected 0 files indexed (large file should be skipped), got %d", len(result.Files))
	}
}

func TestCodiveIgnore(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codive_ignore_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ignoreContent := `# Ignore log files
*.log
# Ignore temp directory
temp/
# Ignore secret files
.env
`
	if err := os.WriteFile(filepath.Join(tempDir, ".codiveignore"), []byte(ignoreContent), 0644); err != nil {
		t.Fatalf("failed to write .codiveignore: %v", err)
	}

	// Create test files
	os.WriteFile(filepath.Join(tempDir, "keep.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(tempDir, "app.log"), []byte("log data"), 0644)
	os.WriteFile(filepath.Join(tempDir, ".env"), []byte("SECRET=123"), 0644)
	os.MkdirAll(filepath.Join(tempDir, "temp"), 0755)
	os.WriteFile(filepath.Join(tempDir, "temp", "cache.json"), []byte("{}"), 0644)

	result, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("expected exactly 1 file (keep.go), got %d files", len(result.Files))
	}
	if result.Files[0].Path != "keep.go" {
		t.Errorf("expected keep.go, got %s", result.Files[0].Path)
	}
}

func TestScanIncremental(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codive_incr_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	file1 := filepath.Join(tempDir, "file1.go")
	file2 := filepath.Join(tempDir, "file2.go")
	os.WriteFile(file1, []byte("content 1"), 0644)
	os.WriteFile(file2, []byte("content 2"), 0644)

	initialResult, err := Scan(tempDir)
	if err != nil {
		t.Fatalf("initial scan failed: %v", err)
	}

	existing := make(map[string]db.FileRecord)
	for _, f := range initialResult.Files {
		existing[f.Path] = f
	}

	// 1. Add file3.go
	file3 := filepath.Join(tempDir, "file3.go")
	os.WriteFile(file3, []byte("content 3"), 0644)

	// 2. Modify file2.go
	os.WriteFile(file2, []byte("content 2 modified!"), 0644)

	// 3. Delete file1.go
	os.Remove(file1)

	// Run incremental scan
	incrResult, err := ScanIncremental(tempDir, existing)
	if err != nil {
		t.Fatalf("incremental scan failed: %v", err)
	}

	if len(incrResult.Added) != 1 || incrResult.Added[0].Path != "file3.go" {
		t.Errorf("expected 1 added file (file3.go), got %+v", incrResult.Added)
	}
	if len(incrResult.Modified) != 1 || incrResult.Modified[0].Path != "file2.go" {
		t.Errorf("expected 1 modified file (file2.go), got %+v", incrResult.Modified)
	}
	if len(incrResult.Deleted) != 1 || incrResult.Deleted[0] != "file1.go" {
		t.Errorf("expected 1 deleted file (file1.go), got %+v", incrResult.Deleted)
	}
}

