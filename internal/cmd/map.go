// Package cmd implements the command line actions and subcommands for codive.
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/ui"
	"github.com/recscse/codive/internal/web"
)

// FileMapJSON represents a single file and its symbols for JSON serialization.
type FileMapJSON struct {
	Path      string            `json:"path"`
	Language  string            `json:"language"`
	SizeBytes int64             `json:"size_bytes"`
	Symbols   []db.SymbolRecord `json:"symbols,omitempty"`
}

// RepoMapJSON represents the complete structural map output in JSON.
type RepoMapJSON struct {
	TotalFiles   int           `json:"total_files"`
	TotalSymbols int           `json:"total_symbols"`
	Files        []FileMapJSON `json:"files"`
}

// MapOptions configures repository map generation.
type MapOptions struct {
	MaxDepth        int
	IncludeSymbols  bool
	DirectoryFilter string
	TokenBudget     int // Maximum approximate tokens (default 2500)
	AsJSON          bool
}

// RunMap prints a hierarchical structural map of the repository with key symbols.
func RunMap(targetDir string, asJSON bool) error {
	return RunMapWithOptions(targetDir, MapOptions{
		IncludeSymbols: true,
		TokenBudget:    4000,
		AsJSON:         asJSON,
	})
}

// RunMapWeb launches the interactive browser web map on localhost:7890.
func RunMapWeb(targetDir string, port int) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".codive", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("repository is not initialized (no index found at %s). Run 'codive init' first", dbPath)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	return web.StartWebServer(absDir, database, port)
}

// RunMapWithOptions generates and outputs a structural repository map with custom depth/filter options.
func RunMapWithOptions(targetDir string, opts MapOptions) error {
	output, err := GenerateMap(targetDir, opts)
	if err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}

// GenerateMap returns the string representation of the repository map with token-budget protection.
func GenerateMap(targetDir string, opts MapOptions) (string, error) {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return "", fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".codive", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return "", fmt.Errorf("repository is not initialized (no index found at %s). Run 'codive init' first", dbPath)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	ctx := context.Background()
	allFiles, err := db.GetAllFiles(ctx, database)
	if err != nil {
		return "", fmt.Errorf("failed to get files: %w", err)
	}

	allSymbols, err := db.GetAllSymbols(ctx, database)
	if err != nil {
		return "", fmt.Errorf("failed to get symbols: %w", err)
	}

	// Group symbols by file path
	symbolsByFile := make(map[string][]db.SymbolRecord)
	if opts.IncludeSymbols {
		for _, s := range allSymbols {
			symbolsByFile[s.FilePath] = append(symbolsByFile[s.FilePath], s)
		}
	}

	// Filter and sort file paths
	filterNorm := filepath.ToSlash(opts.DirectoryFilter)
	filterNorm = strings.Trim(filterNorm, "/")

	var filePaths []string
	for p := range allFiles {
		pNorm := filepath.ToSlash(p)
		if filterNorm != "" && !strings.HasPrefix(pNorm, filterNorm) {
			continue
		}

		if opts.MaxDepth > 0 {
			depth := len(strings.Split(pNorm, "/"))
			if depth > opts.MaxDepth {
				continue
			}
		}

		filePaths = append(filePaths, p)
	}

	// Importance sort: Entrypoints & root files first, then alphabetical
	sort.Slice(filePaths, func(i, j int) bool {
		pi := filePaths[i]
		pj := filePaths[j]
		di := strings.Count(filepath.ToSlash(pi), "/")
		dj := strings.Count(filepath.ToSlash(pj), "/")
		if di != dj {
			return di < dj
		}
		return pi < pj
	})

	if opts.AsJSON {
		var filesJSON []FileMapJSON
		for _, p := range filePaths {
			rec := allFiles[p]
			filesJSON = append(filesJSON, FileMapJSON{
				Path:      p,
				Language:  rec.Language,
				SizeBytes: rec.SizeBytes,
				Symbols:   symbolsByFile[p],
			})
		}
		var buf strings.Builder
		_ = ui.PrintJSON(RepoMapJSON{
			TotalFiles:   len(filePaths),
			TotalSymbols: len(allSymbols),
			Files:        filesJSON,
		})
		return buf.String(), nil
	}

	// Group files by directory
	dirMap := make(map[string][]string)
	for _, p := range filePaths {
		dir := filepath.Dir(p)
		if dir == "." {
			dir = "(root)"
		}
		dirMap[dir] = append(dirMap[dir], p)
	}

	var dirs []string
	for d := range dirMap {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	maxChars := 16000 // ~4000 tokens default
	if opts.TokenBudget > 0 {
		maxChars = opts.TokenBudget * 4
	}

	var sb strings.Builder
	sb.WriteString("# Repository Map\n")
	sb.WriteString(fmt.Sprintf("Total Files: %d | Total Symbols: %d\n\n", len(filePaths), len(allSymbols)))

	truncatedFiles := 0

	for _, d := range dirs {
		if sb.Len() > maxChars {
			truncatedFiles += len(dirMap[d])
			continue
		}

		sb.WriteString(fmt.Sprintf("## %s\n", d))
		for _, f := range dirMap[d] {
			if sb.Len() > maxChars {
				truncatedFiles++
				continue
			}

			fileRec := allFiles[f]
			syms := symbolsByFile[f]
			if len(syms) == 0 || !opts.IncludeSymbols {
				sb.WriteString(fmt.Sprintf("  - `%s` (%s, %s)\n",
					filepath.Base(f),
					fileRec.Language,
					formatBytes(fileRec.SizeBytes)))
			} else {
				sb.WriteString(fmt.Sprintf("  - `%s` (%s, %s) — %s:\n",
					filepath.Base(f),
					fileRec.Language,
					formatBytes(fileRec.SizeBytes),
					ui.Count(len(syms), "symbol", "symbols")))
				for _, s := range syms {
					cleanSig := strings.TrimSpace(s.Signature)
					if cleanSig == "" {
						cleanSig = s.Name
					}
					sb.WriteString(fmt.Sprintf("     • [%s] `%s` (L%d)\n",
						s.Kind,
						cleanSig,
						s.LineNumber))
				}
			}
		}
		sb.WriteString("\n")
	}

	if truncatedFiles > 0 {
		sb.WriteString(fmt.Sprintf("\n> Map truncated (+%s omitted) to fit token budget. Use 'directory_filter' or 'max_depth' to inspect specific submodules.\n", ui.Count(truncatedFiles, "file", "files")))
	}

	return strings.TrimSpace(sb.String()), nil
}
