// Package scanner handles recursive directory traversal, ignore filtering, language detection, and hashing.
package scanner

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/recscse/codive/internal/db"
)

const (
	// MaxFileSize is the maximum file size (5MB) indexed by codive.
	MaxFileSize = 5 * 1024 * 1024

	// SniffBufferSize is the number of initial bytes checked for binary content.
	SniffBufferSize = 8000
)

// DefaultIgnoredDirectories contains directory names that should be skipped during scan.
var DefaultIgnoredDirectories = map[string]bool{
	".git":              true,
	".svn":              true,
	".hg":               true,
	".codive":           true,
	".idea":             true,
	".vscode":           true,
	"node_modules":      true,
	"vendor":            true,
	"build":             true,
	"dist":              true,
	"target":            true,
	"bin":               true,
	"obj":               true,
	".venv":             true,
	"venv":              true,
	"env":               true,
	"__pycache__":       true,
	".pytest_cache":     true,
	".mypy_cache":       true,
	".gradle":           true,
	".mvn":              true,
	"coverage":          true,
	".next":             true,
	".nuxt":             true,
	"jacoco-aggregator": true,
}

// DefaultIgnoredFiles contains file names that should be skipped during scan.
var DefaultIgnoredFiles = map[string]bool{
	".codiveignore": true,
}

// ExtensionLanguageMap maps file extensions to language names.
var ExtensionLanguageMap = map[string]string{
	".go":       "Go",
	".py":       "Python",
	".pyw":      "Python",
	".ts":       "TypeScript",
	".tsx":      "TypeScript",
	".js":       "JavaScript",
	".jsx":      "JavaScript",
	".mjs":      "JavaScript",
	".cjs":      "JavaScript",
	".java":     "Java",
	".cs":       "C#",
	".c":        "C",
	".h":        "C",
	".cpp":      "C++",
	".cc":       "C++",
	".cxx":      "C++",
	".hpp":      "C++",
	".hxx":      "C++",
	".hh":       "C++",
	".rs":       "Rust",
	".rb":       "Ruby",
	".php":      "PHP",
	".swift":    "Swift",
	".kt":       "Kotlin",
	".kts":      "Kotlin",
	".scala":    "Scala",
	".html":     "HTML",
	".htm":      "HTML",
	".css":      "CSS",
	".scss":     "CSS",
	".sass":     "CSS",
	".less":     "CSS",
	".json":     "JSON",
	".yaml":     "YAML",
	".yml":      "YAML",
	".toml":     "TOML",
	".xml":      "XML",
	".sql":      "SQL",
	".md":       "Markdown",
	".markdown": "Markdown",
	".sh":       "Shell",
	".bash":     "Shell",
	".zsh":      "Shell",
	".ps1":      "PowerShell",
	".psm1":     "PowerShell",
}

// DetectLanguage identifies the programming / markup language based on file extension.
func DetectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	if lang, ok := ExtensionLanguageMap[ext]; ok {
		return lang
	}
	return "Text"
}

// LoadIgnorePatterns loads custom ignore rules from .codiveignore if present in rootDir.
func LoadIgnorePatterns(rootDir string) ([]string, error) {
	ignorePath := filepath.Join(rootDir, ".codiveignore")
	f, err := os.Open(ignorePath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, filepath.ToSlash(line))
	}
	return patterns, scanner.Err()
}

// MatchesPattern checks whether a relative path matches an ignore pattern.
func MatchesPattern(relPath string, isDir bool, pattern string) bool {
	pattern = strings.TrimPrefix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")

	// Match exact filename or directory name
	base := filepath.Base(relPath)
	if matched, _ := filepath.Match(pattern, base); matched {
		return true
	}

	// Match full relative path
	if matched, _ := filepath.Match(pattern, relPath); matched {
		return true
	}

	// Match directory prefix
	if strings.HasPrefix(relPath, pattern+"/") {
		return true
	}

	return false
}

// IsBinary checks whether a file is binary by inspecting its initial byte slice for null bytes.
func IsBinary(filePath string) (bool, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, SniffBufferSize)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}

	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true, nil
		}
	}
	return false, nil
}

type fileStamp struct {
	size    int64
	modTime time.Time
}

// binaryCache remembers files found to be binary, keyed by absolute path, so
// repeated scans (the serve/watch loops) don't reopen them while their size
// and mtime are unchanged. It only holds binary files, so it stays small.
var binaryCache = struct {
	sync.Mutex
	m map[string]fileStamp
}{m: make(map[string]fileStamp)}

func knownBinary(path string, info fs.FileInfo) bool {
	binaryCache.Lock()
	defer binaryCache.Unlock()
	st, ok := binaryCache.m[path]
	return ok && st.size == info.Size() && st.modTime.Equal(info.ModTime())
}

func rememberBinary(path string, info fs.FileInfo) {
	binaryCache.Lock()
	defer binaryCache.Unlock()
	binaryCache.m[path] = fileStamp{size: info.Size(), modTime: info.ModTime()}
}

// HashFile computes the hex SHA-256 hash of a file's content.
func HashFile(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// HashBytes computes the hex SHA-256 hash of in-memory content, matching HashFile.
func HashBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// ScanResult contains the collection of scanned files and language summary.
type ScanResult struct {
	Files          []db.FileRecord
	LanguageCounts map[string]int
	TotalSizeBytes int64
}

// Scan walks rootDir recursively, filtering ignored dirs, binary files, and large files.
func Scan(rootDir string) (*ScanResult, error) {
	incremental, err := ScanIncremental(rootDir, nil)
	if err != nil {
		return nil, err
	}

	allFiles := append(incremental.Added, incremental.Modified...)
	return &ScanResult{
		Files:          allFiles,
		LanguageCounts: incremental.LanguageCounts,
		TotalSizeBytes: incremental.TotalSizeBytes,
	}, nil
}

// IncrementalResult contains categorized changes detected between disk and index.
type IncrementalResult struct {
	Added    []db.FileRecord
	Modified []db.FileRecord
	Deleted  []string
	// MetadataOnly holds files whose mtime changed on disk but whose content
	// hash is identical to what's indexed (e.g. a touch, or a checkout that
	// resets timestamps). Their symbols/FTS content don't need re-extracting,
	// but callers should still persist these records via db.SaveFiles so the
	// stored LastModified is refreshed — otherwise the fast unchanged-check in
	// the next scan keeps failing for these files and they get rehashed from
	// scratch on every single scan, forever.
	MetadataOnly   []db.FileRecord
	UnchangedCount int
	LanguageCounts map[string]int
	TotalSizeBytes int64
}

// ScanIncremental detects added, modified, unchanged, and deleted files compared to existing.
func ScanIncremental(rootDir string, existing map[string]db.FileRecord) (*IncrementalResult, error) {
	now := time.Now().UTC()
	result := &IncrementalResult{
		Added:          make([]db.FileRecord, 0),
		Modified:       make([]db.FileRecord, 0),
		Deleted:        make([]string, 0),
		LanguageCounts: make(map[string]int),
	}

	cleanRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}

	customPatterns, err := LoadIgnorePatterns(cleanRoot)
	if err != nil {
		return nil, err
	}

	seenOnDisk := make(map[string]bool)

	err = filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// The root itself being unreadable is fatal; anything below it
			// (a permission-denied subdirectory, a file removed mid-walk) is
			// skipped rather than aborting the whole scan.
			if path == cleanRoot {
				return walkErr
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(cleanRoot, path)
		if err != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)

		if d.IsDir() {
			if DefaultIgnoredDirectories[d.Name()] {
				return filepath.SkipDir
			}
			for _, pat := range customPatterns {
				if MatchesPattern(relPath, true, pat) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Skip sockets, devices, and named pipes (opening a FIFO to sniff it
		// would block the scan). Symlinked files are still indexed, as before.
		if t := d.Type(); !t.IsRegular() && t&fs.ModeSymlink == 0 {
			return nil
		}

		// Check default file ignore
		if DefaultIgnoredFiles[d.Name()] {
			return nil
		}

		// Check file ignore patterns
		for _, pat := range customPatterns {
			if MatchesPattern(relPath, false, pat) {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		// Skip files larger than 5MB
		if info.Size() > MaxFileSize {
			return nil
		}

		existingRec, exists := existing[relPath]
		lang := DetectLanguage(path)

		// Check if file is unchanged based on size and modtime. This runs
		// before the binary sniff on purpose: an indexed file already passed
		// that check, and skipping it here means a no-op rescan (the serve
		// watcher runs one every few seconds) stats files without opening them.
		if exists && existingRec.SizeBytes == info.Size() && existingRec.LastModified.Equal(info.ModTime().UTC()) {
			seenOnDisk[relPath] = true
			result.LanguageCounts[lang]++
			result.TotalSizeBytes += info.Size()
			result.UnchangedCount++
			return nil
		}

		// Check if file is binary (unless 0 bytes). Files already known to be
		// binary at this size/mtime are skipped without being reopened: they
		// never enter the index, so the unchanged check above can't catch
		// them, and re-sniffing every one on each watcher pass dominated
		// no-op rescans of large repos.
		if info.Size() > 0 {
			if knownBinary(path, info) {
				return nil
			}
			binary, err := IsBinary(path)
			if err != nil {
				return nil
			}
			if binary {
				rememberBinary(path, info)
				return nil
			}
		}

		seenOnDisk[relPath] = true
		result.LanguageCounts[lang]++
		result.TotalSizeBytes += info.Size()

		// Compute hash
		var hash string
		if info.Size() == 0 {
			hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		} else {
			hash, err = HashFile(path)
			if err != nil {
				return nil
			}
		}

		record := db.FileRecord{
			Path:         relPath,
			Language:     lang,
			SizeBytes:    info.Size(),
			ContentHash:  hash,
			LastModified: info.ModTime().UTC(),
			LastIndexed:  now,
		}

		if !exists {
			result.Added = append(result.Added, record)
		} else if existingRec.ContentHash != hash {
			result.Modified = append(result.Modified, record)
		} else {
			// Hash is identical despite the mtime change: content didn't
			// actually change, so this doesn't need symbol/FTS re-extraction,
			// but the stored LastModified still needs refreshing (see
			// MetadataOnly's doc comment) or every future scan re-hashes it.
			result.MetadataOnly = append(result.MetadataOnly, record)
			result.UnchangedCount++
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Identify deleted files
	if existing != nil {
		for oldPath := range existing {
			if !seenOnDisk[oldPath] {
				result.Deleted = append(result.Deleted, oldPath)
			}
		}
	}

	return result, nil
}
