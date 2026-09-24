// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/symbols"
	"github.com/recscse/codive/internal/ui"
)

// formatBytes converts byte count to human-readable format.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// IndexResult summarizes the outcome of indexing a repository.
type IndexResult struct {
	TotalFiles     int
	TotalSizeBytes int64
	SymbolCount    int
	LanguageCounts map[string]int
	PrimaryLang    string
	DBPath         string
	Duration       time.Duration
}

// indexRepository scans absDir and populates its .codive/index.db. It performs
// no output of its own — onFile (may be nil) is invoked once per file as
// results come in, so callers where stdout must carry nothing but a protocol
// stream (like the MCP server) can safely pass nil.
func indexRepository(absDir string, onFile func(processed, total int, path string)) (*IndexResult, error) {
	startTime := time.Now()

	scanResult, err := scanner.Scan(absDir)
	if err != nil {
		return nil, fmt.Errorf("scan failed: %w", err)
	}

	codiveDir := filepath.Join(absDir, ".codive")
	dbPath := filepath.Join(codiveDir, "index.db")

	database, err := db.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	if err := db.InitSchema(database); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	ctx := context.Background()
	// init is a full rebuild: without clearing first, files deleted since the
	// last index and symbols whose line number moved (line_number is part of
	// the symbols primary key) would survive alongside the fresh rows.
	if err := db.ClearIndex(ctx, database); err != nil {
		return nil, fmt.Errorf("failed to clear previous index: %w", err)
	}
	if err := db.SaveFiles(ctx, database, scanResult.Files); err != nil {
		return nil, fmt.Errorf("failed to save file records: %w", err)
	}

	totalFiles := len(scanResult.Files)
	allSymbols := make([]db.SymbolRecord, 0, totalFiles*5)
	ftsFiles := make(map[string]string, totalFiles)

	if totalFiles > 0 {
		numWorkers := runtime.NumCPU() * 2
		if numWorkers < 4 {
			numWorkers = 4
		}
		if numWorkers > 32 {
			numWorkers = 32
		}

		type parseResult struct {
			path    string
			content string
			symbols []db.SymbolRecord
		}

		fileChan := make(chan db.FileRecord, totalFiles)
		resultChan := make(chan parseResult, totalFiles)

		var wg sync.WaitGroup
		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for f := range fileChan {
					fullPath := filepath.Join(absDir, filepath.FromSlash(f.Path))
					content, err := os.ReadFile(fullPath)
					if err != nil {
						resultChan <- parseResult{path: f.Path}
						continue
					}
					syms, _ := symbols.ExtractSymbols(f.Path, f.Language, content)
					resultChan <- parseResult{
						path:    f.Path,
						content: string(content),
						symbols: syms,
					}
				}
			}()
		}

		for _, f := range scanResult.Files {
			fileChan <- f
		}
		close(fileChan)

		go func() {
			wg.Wait()
			close(resultChan)
		}()

		processed := 0
		for res := range resultChan {
			processed++
			if onFile != nil {
				onFile(processed, totalFiles, res.path)
			}
			if res.content != "" {
				ftsFiles[res.path] = res.content
			}
			if len(res.symbols) > 0 {
				allSymbols = append(allSymbols, res.symbols...)
			}
		}
	}

	if len(allSymbols) > 0 {
		if err := db.SaveSymbols(ctx, database, allSymbols); err != nil {
			return nil, fmt.Errorf("failed to save symbols: %w", err)
		}
	}

	if len(ftsFiles) > 0 {
		if err := db.SaveFTS(ctx, database, ftsFiles); err != nil {
			return nil, fmt.Errorf("failed to save full-text search index: %w", err)
		}
	}

	// Determine primary language(s)
	type langCount struct {
		name  string
		count int
	}
	var sortedLangs []langCount
	for l, c := range scanResult.LanguageCounts {
		sortedLangs = append(sortedLangs, langCount{name: l, count: c})
	}
	sort.Slice(sortedLangs, func(i, j int) bool {
		if sortedLangs[i].count == sortedLangs[j].count {
			return sortedLangs[i].name < sortedLangs[j].name
		}
		return sortedLangs[i].count > sortedLangs[j].count
	})

	primaryLang := "None"
	if len(sortedLangs) > 0 {
		primaryLang = sortedLangs[0].name
	}

	return &IndexResult{
		TotalFiles:     totalFiles,
		TotalSizeBytes: scanResult.TotalSizeBytes,
		SymbolCount:    len(allSymbols),
		LanguageCounts: scanResult.LanguageCounts,
		PrimaryLang:    primaryLang,
		DBPath:         dbPath,
		Duration:       time.Since(startTime),
	}, nil
}

// RunInit initializes the repository index by scanning files and populating .codive/index.db.
func RunInit(targetDir string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	stat, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("cannot access directory %s: %w", absDir, err)
	}
	if !stat.IsDir() {
		return fmt.Errorf("path %s is not a directory", absDir)
	}

	slog.Info("Starting repository initialization", "path", absDir)

	var bar *ui.ProgressBar
	result, err := indexRepository(absDir, func(processed, total int, path string) {
		if bar == nil {
			bar = ui.NewProgressBar(total, "Indexing Codebase", "files")
		}
		bar.Update(1, path)
	})
	if err != nil {
		slog.Error("Failed to index repository", "error", err)
		return err
	}
	if bar != nil {
		bar.Finish("Symbols & AST extracted")
	}

	fmt.Println()
	ui.Header("codive — Repository Index Initialization")
	ui.Divider()
	ui.KeyValueHighlight("Indexed Files", fmt.Sprintf("%s (%s)", ui.Count(result.TotalFiles, "file", "files"), formatBytes(result.TotalSizeBytes)))
	ui.KeyValueHighlight("AST Symbols", ui.Count(result.SymbolCount, "symbol", "symbols"))
	ui.KeyValue("Language", result.PrimaryLang)
	ui.KeyValue("Database Path", result.DBPath)
	ui.KeyValue("Latency", fmt.Sprintf("%v", result.Duration.Round(time.Millisecond)))
	ui.Divider()
	fmt.Println()
	ui.Success("Repository successfully indexed into local SQLite (WAL mode)!")
	fmt.Println()

	return nil
}

// RunInitSilent performs the same indexing as RunInit but produces no output
// of any kind. Use it from contexts — like the MCP stdio server — where
// stdout must carry nothing but the protocol stream: any human-readable text
// written there would corrupt the JSON-RPC transport for whichever client is
// reading it.
func RunInitSilent(targetDir string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	stat, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("cannot access directory %s: %w", absDir, err)
	}
	if !stat.IsDir() {
		return fmt.Errorf("path %s is not a directory", absDir)
	}

	slog.Info("Silently auto-indexing repository for MCP server", "path", absDir)
	_, err = indexRepository(absDir, nil)
	if err != nil {
		slog.Error("Silent auto-index failed", "error", err)
	}
	return err
}
