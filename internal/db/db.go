// Package db manages the SQLite database, schema migrations, and queries for codive.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// FileRecord represents a scanned source file stored in SQLite.
type FileRecord struct {
	Path         string
	Language     string
	SizeBytes    int64
	ContentHash  string
	LastModified time.Time
	LastIndexed  time.Time
}

// SymbolRecord represents an extracted code symbol (function, method, class, struct, etc.).
type SymbolRecord struct {
	FilePath   string
	Name       string
	Kind       string
	Signature  string
	LineNumber int
}

// RepoStats contains aggregate statistics from the index.
type RepoStats struct {
	TotalFiles     int
	TotalSizeBytes int64
	LastUpdated    time.Time
	LanguageCounts map[string]int
}

// CurrentSchemaVersion is the latest database schema version.
const CurrentSchemaVersion = 7

// Open initializes and opens the SQLite database at dbPath, creating parent dirs and migrating schema.
func Open(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	// Modernc sqlite supports DSN query parameters for busy_timeout, journal_mode, and sync
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-64000)", filepath.ToSlash(dbPath))

	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Limit idle connections to prevent holding lock during restarts
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(2)
	database.SetConnMaxLifetime(10 * time.Minute)

	// Ensure WAL & busy timeout with retry if another process is briefly finishing
	var pragmaErr error
	for attempt := 0; attempt < 5; attempt++ {
		_, pragmaErr = database.Exec("PRAGMA busy_timeout = 10000; PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL;")
		if pragmaErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Automatically migrate schema on open
	if err := Migrate(database); err != nil {
		database.Close()
		return nil, fmt.Errorf("database schema migration failed: %w\nSuggestion: run 'codive init' to rebuild the index from scratch", err)
	}

	return database, nil
}

// OpenRaw opens a SQLite database connection without executing schema migrations (useful for testing).
func OpenRaw(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}
	return sql.Open("sqlite", dbPath)
}

// GetSchemaVersion returns the current user_version from SQLite PRAGMA.
func GetSchemaVersion(database *sql.DB) (int, error) {
	var version int
	row := database.QueryRow("PRAGMA user_version;")
	if err := row.Scan(&version); err != nil {
		return 0, fmt.Errorf("failed to read schema version: %w", err)
	}
	return version, nil
}

// SetSchemaVersion updates the user_version PRAGMA in SQLite.
func SetSchemaVersion(database *sql.DB, version int) error {
	_, err := database.Exec(fmt.Sprintf("PRAGMA user_version = %d;", version))
	if err != nil {
		return fmt.Errorf("failed to set schema version to %d: %w", version, err)
	}
	return nil
}

// Migration represents an incremental schema change.
type Migration struct {
	Version     int
	Description string
	SQL         string
}

// Migrations contains all incremental migrations in order.
var Migrations = []Migration{
	{
		Version:     1,
		Description: "Create files table and index",
		SQL: `
		CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			language TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			content_hash TEXT NOT NULL,
			last_modified TIMESTAMP NOT NULL,
			last_indexed TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_files_language ON files(language);
		`,
	},
	{
		Version:     2,
		Description: "Create symbols table and indexes",
		SQL: `
		CREATE TABLE IF NOT EXISTS symbols (
			file_path TEXT NOT NULL,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			signature TEXT NOT NULL,
			line_number INTEGER NOT NULL,
			PRIMARY KEY (file_path, name, kind, line_number)
		);
		CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
		CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_path);
		`,
	},
	{
		Version:     3,
		Description: "Create file_fts virtual table for full-text search",
		SQL: `
		CREATE VIRTUAL TABLE IF NOT EXISTS file_fts USING fts5(
			path UNINDEXED,
			content,
			tokenize = 'porter unicode61'
		);
		`,
	},
	{
		Version:     4,
		Description: "Create decisions table for persistent agent memory",
		SQL: `
		CREATE TABLE IF NOT EXISTS decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			topic TEXT NOT NULL,
			summary TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_decisions_topic ON decisions(topic);
		`,
	},
	{
		Version:     5,
		Description: "Create telemetry table for token & money savings tracker",
		SQL: `
		CREATE TABLE IF NOT EXISTS telemetry (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tool_name TEXT NOT NULL,
			tokens_saved INTEGER NOT NULL,
			latency_saved_ms INTEGER NOT NULL,
			created_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_telemetry_created ON telemetry(created_at);
		`,
	},
	{
		Version:     6,
		Description: "Add raw_estimate to telemetry so speed multiplier is computed from real data",
		SQL: `
		ALTER TABLE telemetry ADD COLUMN raw_estimate INTEGER NOT NULL DEFAULT 0;
		`,
	},
	{
		Version:     7,
		Description: "Create meta table for index-wide settings such as the extractor version",
		SQL: `
		CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		`,
	},
}

// GetMeta returns an index-wide setting, or "" if it has never been set.
func GetMeta(ctx context.Context, database *sql.DB, key string) (string, error) {
	var v string
	err := database.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?;", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetMeta stores an index-wide setting.
func SetMeta(ctx context.Context, database *sql.DB, key, value string) error {
	_, err := database.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value;
	`, key, value)
	return err
}

// Migrate applies all pending schema migrations up to CurrentSchemaVersion.
func Migrate(database *sql.DB) error {
	currentVersion, err := GetSchemaVersion(database)
	if err != nil {
		return err
	}

	for _, m := range Migrations {
		if currentVersion < m.Version {
			if err := applyMigration(database, m); err != nil {
				return err
			}
		}
	}

	return nil
}

// applyMigration runs a migration's SQL and records its version in a single
// transaction (SQLite DDL and user_version are both transactional). Doing them
// separately meant a crash in between left a non-idempotent migration such as
// ALTER TABLE ADD COLUMN applied but unrecorded, so every later Open retried
// it and failed with "duplicate column".
func applyMigration(database *sql.DB, m Migration) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("migration %d: failed to begin transaction: %w", m.Version, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(m.SQL); err != nil {
		return fmt.Errorf("migration %d failed (%s): %w", m.Version, m.Description, err)
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d;", m.Version)); err != nil {
		return fmt.Errorf("failed to set schema version to %d: %w", m.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %d: failed to commit: %w", m.Version, err)
	}
	return nil
}

// InitSchema ensures all migrations are applied.
func InitSchema(database *sql.DB) error {
	return Migrate(database)
}

// SaveFiles batch-upserts file records into the files table.
func SaveFiles(ctx context.Context, database *sql.DB, files []FileRecord) error {
	if len(files) == 0 {
		return nil
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
	INSERT INTO files (path, language, size_bytes, content_hash, last_modified, last_indexed)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		language = excluded.language,
		size_bytes = excluded.size_bytes,
		content_hash = excluded.content_hash,
		last_modified = excluded.last_modified,
		last_indexed = excluded.last_indexed;
	`
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to prepare insert statement: %w", err)
	}
	defer stmt.Close()

	for _, f := range files {
		_, err := stmt.ExecContext(
			ctx,
			f.Path,
			f.Language,
			f.SizeBytes,
			f.ContentHash,
			f.LastModified.UTC(),
			f.LastIndexed.UTC(),
		)
		if err != nil {
			return fmt.Errorf("failed to insert record for %s: %w", f.Path, err)
		}
	}

	return tx.Commit()
}

// GetStats returns aggregated statistics from the indexed files.
func GetStats(ctx context.Context, database *sql.DB) (*RepoStats, error) {
	stats := &RepoStats{
		LanguageCounts: make(map[string]int),
	}

	// 1. Total files, total size, and max last_indexed
	var maxIndexedStr sql.NullString
	var totalFiles sql.NullInt64
	var totalSize sql.NullInt64

	row := database.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0), MAX(last_indexed)
		FROM files;
	`)
	if err := row.Scan(&totalFiles, &totalSize, &maxIndexedStr); err != nil {
		return nil, fmt.Errorf("failed to query file summary: %w", err)
	}

	stats.TotalFiles = int(totalFiles.Int64)
	stats.TotalSizeBytes = totalSize.Int64
	if maxIndexedStr.Valid && maxIndexedStr.String != "" {
		// Attempt common SQLite / Go timestamp layouts
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05.999999999 -0700 MST",
			"2006-01-02 15:04:05.999999999+00:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05",
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, maxIndexedStr.String); err == nil {
				stats.LastUpdated = t
				break
			}
		}
	}

	// 2. Language breakdown
	rows, err := database.QueryContext(ctx, `
		SELECT language, COUNT(*) 
		FROM files 
		GROUP BY language 
		ORDER BY COUNT(*) DESC, language ASC;
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query language counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var lang string
		var count int
		if err := rows.Scan(&lang, &count); err != nil {
			return nil, fmt.Errorf("failed to scan language count: %w", err)
		}
		stats.LanguageCounts[lang] = count
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading language count rows: %w", err)
	}

	return stats, nil
}

func parseTimestamp(val string) time.Time {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999+00:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, val); err == nil {
			return t
		}
	}
	return time.Time{}
}

// GetAllFiles returns all currently indexed file records keyed by relative path.
func GetAllFiles(ctx context.Context, database *sql.DB) (map[string]FileRecord, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT path, language, size_bytes, content_hash, last_modified, last_indexed
		FROM files;
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query all files: %w", err)
	}
	defer rows.Close()

	records := make(map[string]FileRecord)
	for rows.Next() {
		var r FileRecord
		var lastModStr, lastIdxStr string
		if err := rows.Scan(&r.Path, &r.Language, &r.SizeBytes, &r.ContentHash, &lastModStr, &lastIdxStr); err != nil {
			return nil, fmt.Errorf("failed to scan file record: %w", err)
		}
		r.LastModified = parseTimestamp(lastModStr)
		r.LastIndexed = parseTimestamp(lastIdxStr)
		records[r.Path] = r
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating file records: %w", err)
	}

	return records, nil
}

// GetFile returns the indexed record for a single file path. The bool result
// is false (with a nil error) when the path isn't in the index.
func GetFile(ctx context.Context, database *sql.DB, path string) (FileRecord, bool, error) {
	var r FileRecord
	var lastModStr, lastIdxStr string
	err := database.QueryRowContext(ctx, `
		SELECT path, language, size_bytes, content_hash, last_modified, last_indexed
		FROM files
		WHERE path = ?;
	`, path).Scan(&r.Path, &r.Language, &r.SizeBytes, &r.ContentHash, &lastModStr, &lastIdxStr)
	if err == sql.ErrNoRows {
		return FileRecord{}, false, nil
	}
	if err != nil {
		return FileRecord{}, false, fmt.Errorf("failed to query file %s: %w", path, err)
	}
	r.LastModified = parseTimestamp(lastModStr)
	r.LastIndexed = parseTimestamp(lastIdxStr)
	return r, true, nil
}

// IndexChanges is one batch of index updates, applied atomically by
// ApplyIndexChanges.
type IndexChanges struct {
	// ReplaceAll clears every file, symbol, and FTS row first, making the
	// batch a full rebuild. Decisions and telemetry are always kept: they
	// aren't derived from the source.
	ReplaceAll bool
	// Files are added or modified files. Their existing symbols and FTS rows
	// are replaced by the Symbols and FTS entries in this batch.
	Files   []FileRecord
	Symbols []SymbolRecord
	FTS     map[string]string
	// MetadataOnly are files whose content is unchanged but whose stored
	// size/mtime must be refreshed. Their symbols and FTS rows are untouched.
	MetadataOnly []FileRecord
	// Deleted paths lose their file, symbol, and FTS rows.
	Deleted []string
}

// ApplyIndexChanges writes a batch of index updates in a single transaction.
// A file's record, symbols, and FTS content therefore always change together:
// a crash can't leave a file marked as indexed at its new hash while still
// holding the previous version's symbols, which no later incremental scan
// would notice or repair.
func ApplyIndexChanges(ctx context.Context, database *sql.DB, c IndexChanges) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin index transaction: %w", err)
	}
	defer tx.Rollback()

	exec := func(query string, args ...any) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}

	if c.ReplaceAll {
		for _, table := range []string{"files", "symbols", "file_fts"} {
			if err := exec("DELETE FROM " + table + ";"); err != nil {
				return fmt.Errorf("failed to clear %s: %w", table, err)
			}
		}
	} else {
		stale := make([]string, 0, len(c.Files)+len(c.Deleted))
		for _, f := range c.Files {
			stale = append(stale, f.Path)
		}
		stale = append(stale, c.Deleted...)
		for _, p := range stale {
			if err := exec("DELETE FROM symbols WHERE file_path = ?;", p); err != nil {
				return fmt.Errorf("failed to delete symbols for %s: %w", p, err)
			}
			if err := exec("DELETE FROM file_fts WHERE path = ?;", p); err != nil {
				return fmt.Errorf("failed to delete fts for %s: %w", p, err)
			}
		}
		for _, p := range c.Deleted {
			if err := exec("DELETE FROM files WHERE path = ?;", p); err != nil {
				return fmt.Errorf("failed to delete record for %s: %w", p, err)
			}
		}
	}

	upsertFile, err := tx.PrepareContext(ctx, `
		INSERT INTO files (path, language, size_bytes, content_hash, last_modified, last_indexed)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			language = excluded.language,
			size_bytes = excluded.size_bytes,
			content_hash = excluded.content_hash,
			last_modified = excluded.last_modified,
			last_indexed = excluded.last_indexed;
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare file upsert: %w", err)
	}
	defer upsertFile.Close()
	for _, batch := range [][]FileRecord{c.Files, c.MetadataOnly} {
		for _, f := range batch {
			if _, err := upsertFile.ExecContext(ctx, f.Path, f.Language, f.SizeBytes, f.ContentHash,
				f.LastModified.UTC(), f.LastIndexed.UTC()); err != nil {
				return fmt.Errorf("failed to upsert record for %s: %w", f.Path, err)
			}
		}
	}

	if len(c.Symbols) > 0 {
		insSym, err := tx.PrepareContext(ctx, `
			INSERT INTO symbols (file_path, name, kind, signature, line_number)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(file_path, name, kind, line_number) DO UPDATE SET
				signature = excluded.signature;
		`)
		if err != nil {
			return fmt.Errorf("failed to prepare symbol insert: %w", err)
		}
		defer insSym.Close()
		for _, s := range c.Symbols {
			if _, err := insSym.ExecContext(ctx, s.FilePath, s.Name, s.Kind, s.Signature, s.LineNumber); err != nil {
				return fmt.Errorf("failed to insert symbol %s in %s: %w", s.Name, s.FilePath, err)
			}
		}
	}

	if len(c.FTS) > 0 {
		insFTS, err := tx.PrepareContext(ctx, "INSERT INTO file_fts (path, content) VALUES (?, ?);")
		if err != nil {
			return fmt.Errorf("failed to prepare fts insert: %w", err)
		}
		defer insFTS.Close()
		for p, content := range c.FTS {
			if _, err := insFTS.ExecContext(ctx, p, content); err != nil {
				return fmt.Errorf("failed to insert fts for %s: %w", p, err)
			}
		}
	}

	return tx.Commit()
}

// DeleteFiles removes records for the specified file paths and their associated symbols.
func DeleteFiles(ctx context.Context, database *sql.DB, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin delete transaction: %w", err)
	}
	defer tx.Rollback()

	stmtFiles, err := tx.PrepareContext(ctx, "DELETE FROM files WHERE path = ?;")
	if err != nil {
		return fmt.Errorf("failed to prepare delete files statement: %w", err)
	}
	defer stmtFiles.Close()

	stmtSymbols, err := tx.PrepareContext(ctx, "DELETE FROM symbols WHERE file_path = ?;")
	if err != nil {
		return fmt.Errorf("failed to prepare delete symbols statement: %w", err)
	}
	defer stmtSymbols.Close()

	for _, p := range paths {
		if _, err := stmtSymbols.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("failed to delete symbols for %s: %w", p, err)
		}
		if _, err := stmtFiles.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("failed to delete record for %s: %w", p, err)
		}
	}

	return tx.Commit()
}

// SaveSymbols batch-upserts extracted symbol records.
func SaveSymbols(ctx context.Context, database *sql.DB, symbols []SymbolRecord) error {
	if len(symbols) == 0 {
		return nil
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin symbols transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO symbols (file_path, name, kind, signature, line_number)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(file_path, name, kind, line_number) DO UPDATE SET
			signature = excluded.signature;
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare symbols insert statement: %w", err)
	}
	defer stmt.Close()

	for _, s := range symbols {
		_, err := stmt.ExecContext(ctx, s.FilePath, s.Name, s.Kind, s.Signature, s.LineNumber)
		if err != nil {
			return fmt.Errorf("failed to insert symbol %s in %s: %w", s.Name, s.FilePath, err)
		}
	}

	return tx.Commit()
}

// DeleteSymbolsForFiles removes all symbols for a set of file paths (useful before re-extracting).
func DeleteSymbolsForFiles(ctx context.Context, database *sql.DB, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin delete symbols transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, "DELETE FROM symbols WHERE file_path = ?;")
	if err != nil {
		// If table doesn't exist yet, ignore
		return nil
	}
	defer stmt.Close()

	for _, p := range paths {
		if _, err := stmt.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("failed to delete symbols for %s: %w", p, err)
		}
	}

	return tx.Commit()
}

// GetAllSymbols returns all symbols indexed in the repository.
func GetAllSymbols(ctx context.Context, database *sql.DB) ([]SymbolRecord, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT file_path, name, kind, signature, line_number
		FROM symbols
		ORDER BY file_path ASC, line_number ASC;
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query symbols: %w", err)
	}
	defer rows.Close()

	var symbols []SymbolRecord
	for rows.Next() {
		var s SymbolRecord
		if err := rows.Scan(&s.FilePath, &s.Name, &s.Kind, &s.Signature, &s.LineNumber); err != nil {
			return nil, fmt.Errorf("failed to scan symbol: %w", err)
		}
		symbols = append(symbols, s)
	}
	return symbols, rows.Err()
}

// FindSymbols searches for symbols matching the query string (case-insensitive substring or exact match).
func FindSymbols(ctx context.Context, database *sql.DB, query string) ([]SymbolRecord, error) {
	likePattern := "%" + escapeLike(query) + "%"
	rows, err := database.QueryContext(ctx, `
		SELECT file_path, name, kind, signature, line_number
		FROM symbols
		WHERE name LIKE ? ESCAPE '\' OR signature LIKE ? ESCAPE '\'
		ORDER BY (name = ?) DESC, name ASC, file_path ASC, line_number ASC;
	`, likePattern, likePattern, query)
	if err != nil {
		return nil, fmt.Errorf("failed to find symbols: %w", err)
	}
	defer rows.Close()

	var symbols []SymbolRecord
	for rows.Next() {
		var s SymbolRecord
		if err := rows.Scan(&s.FilePath, &s.Name, &s.Kind, &s.Signature, &s.LineNumber); err != nil {
			return nil, fmt.Errorf("failed to scan symbol: %w", err)
		}
		symbols = append(symbols, s)
	}
	return symbols, rows.Err()
}

// FindSymbolsByName returns symbols whose name is exactly name (case
// sensitive), ordered by file and line.
func FindSymbolsByName(ctx context.Context, database *sql.DB, name string) ([]SymbolRecord, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT file_path, name, kind, signature, line_number
		FROM symbols
		WHERE name = ?
		ORDER BY file_path ASC, line_number ASC;
	`, name)
	if err != nil {
		return nil, fmt.Errorf("failed to find symbol %s: %w", name, err)
	}
	defer rows.Close()

	var syms []SymbolRecord
	for rows.Next() {
		var s SymbolRecord
		if err := rows.Scan(&s.FilePath, &s.Name, &s.Kind, &s.Signature, &s.LineNumber); err != nil {
			return nil, fmt.Errorf("failed to scan symbol: %w", err)
		}
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

// FindSymbolsInFile returns every symbol declared in exactly the given file
// path, ordered by line number. Unlike FindSymbols (a fuzzy name/signature
// search), this is an exact file lookup — the right tool when you already
// know which file you want the declared symbols for.
func FindSymbolsInFile(ctx context.Context, database *sql.DB, filePath string) ([]SymbolRecord, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT file_path, name, kind, signature, line_number
		FROM symbols
		WHERE file_path = ?
		ORDER BY line_number ASC;
	`, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to find symbols in file: %w", err)
	}
	defer rows.Close()

	var syms []SymbolRecord
	for rows.Next() {
		var s SymbolRecord
		if err := rows.Scan(&s.FilePath, &s.Name, &s.Kind, &s.Signature, &s.LineNumber); err != nil {
			return nil, fmt.Errorf("failed to scan symbol: %w", err)
		}
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

// SearchResult represents a matching file from FTS search.
type SearchResult struct {
	Path    string
	Snippet string
	Rank    float64
}

// SaveFTS stores or updates file contents into the full-text search index.
func SaveFTS(ctx context.Context, database *sql.DB, files map[string]string) error {
	if len(files) == 0 {
		return nil
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin fts transaction: %w", err)
	}
	defer tx.Rollback()

	delStmt, err := tx.PrepareContext(ctx, "DELETE FROM file_fts WHERE path = ?;")
	if err != nil {
		return fmt.Errorf("failed to prepare fts delete statement: %w", err)
	}
	defer delStmt.Close()

	insStmt, err := tx.PrepareContext(ctx, "INSERT INTO file_fts (path, content) VALUES (?, ?);")
	if err != nil {
		return fmt.Errorf("failed to prepare fts insert statement: %w", err)
	}
	defer insStmt.Close()

	for path, content := range files {
		if _, err := delStmt.ExecContext(ctx, path); err != nil {
			return fmt.Errorf("failed to clean fts for %s: %w", path, err)
		}
		if _, err := insStmt.ExecContext(ctx, path, content); err != nil {
			return fmt.Errorf("failed to insert fts for %s: %w", path, err)
		}
	}

	return tx.Commit()
}

// DeleteFTSForFiles removes full-text records for the specified file paths.
func DeleteFTSForFiles(ctx context.Context, database *sql.DB, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin fts delete transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, "DELETE FROM file_fts WHERE path = ?;")
	if err != nil {
		return fmt.Errorf("failed to prepare fts delete statement: %w", err)
	}
	defer stmt.Close()

	for _, p := range paths {
		if _, err := stmt.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("failed to delete fts for %s: %w", p, err)
		}
	}

	return tx.Commit()
}

// SearchFTS performs full-text code search across indexed files with symbol-match relevance boost.
func SearchFTS(ctx context.Context, database *sql.DB, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	// Escape special FTS5 characters: quote individual tokens if needed
	ftsQuery := formatFTSQuery(query)

	rows, err := database.QueryContext(ctx, `
		SELECT path, snippet(file_fts, 1, '>>>', '<<<', '...', 15), rank
		FROM file_fts
		WHERE file_fts MATCH ?
		ORDER BY rank ASC
		LIMIT ?;
	`, ftsQuery, limit*2)
	if err != nil {
		return nil, fmt.Errorf("fts query failed: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var res SearchResult
		if err := rows.Scan(&res.Path, &res.Snippet, &res.Rank); err != nil {
			return nil, fmt.Errorf("failed to scan fts result: %w", err)
		}
		results = append(results, res)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Apply Symbol-Boost: prioritize files where the query matches an actual symbol definition
	for i := range results {
		var count int
		row := database.QueryRowContext(ctx, `SELECT COUNT(1) FROM symbols WHERE file_path = ? AND name LIKE ? ESCAPE '\' LIMIT 1;`, results[i].Path, "%"+escapeLike(query)+"%")
		_ = row.Scan(&count)
		if count > 0 {
			results[i].Rank -= 10.0
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Rank < results[j].Rank
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// formatFTSQuery prepares a safe FTS5 MATCH query string.
func formatFTSQuery(raw string) string {
	terms := strings.Fields(raw)
	if len(terms) == 0 {
		return `""`
	}
	var escapedTerms []string
	for _, term := range terms {
		// Clean and double-quote term
		cleaned := strings.ReplaceAll(term, `"`, `""`)
		escapedTerms = append(escapedTerms, fmt.Sprintf(`"%s"`, cleaned))
	}
	return strings.Join(escapedTerms, " ")
}

// GetFileContent retrieves the indexed text content of a file from FTS.
func GetFileContent(ctx context.Context, database *sql.DB, path string) (string, error) {
	var content string
	row := database.QueryRowContext(ctx, "SELECT content FROM file_fts WHERE path = ? LIMIT 1;", path)
	if err := row.Scan(&content); err != nil {
		return "", err
	}
	return content, nil
}

// ReferenceResult represents a reference or call-site of a symbol in the codebase.
type ReferenceResult struct {
	FilePath   string `json:"file_path"`
	LineNumber int    `json:"line_number"`
	Snippet    string `json:"snippet"`
}

// ReferencePage is one page of reference or call-site results.
type ReferencePage struct {
	Refs []ReferenceResult
	// More is true when at least one further match exists beyond Refs, so
	// callers can say "N+ matches" instead of implying Refs is complete.
	More bool
}

// FindCallers narrows FindReferences down to genuine call sites: it excludes the
// symbol's own declaration (using the exact AST-derived location from the symbols
// table, not a text-pattern guess, so this is precise across every supported
// language), excludes comment lines, and keeps only lines that look like an actual
// call expression (`symbol(`, optionally qualified by a receiver/package prefix).
func FindCallers(ctx context.Context, database *sql.DB, symbol string, limit int) ([]ReferenceResult, error) {
	page, err := FindCallersPage(ctx, database, symbol, limit)
	if err != nil {
		return nil, err
	}
	return page.Refs, nil
}

// FindCallersPage is FindCallers plus whether more callers exist past limit.
func FindCallersPage(ctx context.Context, database *sql.DB, symbol string, limit int) (*ReferencePage, error) {
	if limit <= 0 {
		limit = 30
	}

	declSyms, err := FindSymbols(ctx, database, symbol)
	if err != nil {
		return nil, err
	}
	declSites := make(map[string]bool)
	for _, s := range declSyms {
		if s.Name == symbol {
			declSites[fmt.Sprintf("%s:%d", s.FilePath, s.LineNumber)] = true
		}
	}

	// Filtering happens during the scan (rather than over-fetching references
	// and filtering afterwards), so a symbol mentioned mostly in comments or
	// imports can't crowd real call sites out of the result.
	return scanReferences(ctx, database, symbol, limit, func(path string, lineNo int, line string) bool {
		if declSites[fmt.Sprintf("%s:%d", path, lineNo)] {
			return false
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") {
			return false
		}
		return containsCall(line, symbol)
	})
}

// FindReferences scans indexed file contents to locate call sites, imports, and usages of a symbol.
func FindReferences(ctx context.Context, database *sql.DB, symbol string, limit int) ([]ReferenceResult, error) {
	page, err := FindReferencesPage(ctx, database, symbol, limit)
	if err != nil {
		return nil, err
	}
	return page.Refs, nil
}

// FindReferencesPage is FindReferences plus whether more references exist past limit.
func FindReferencesPage(ctx context.Context, database *sql.DB, symbol string, limit int) (*ReferencePage, error) {
	if limit <= 0 {
		limit = 50
	}
	return scanReferences(ctx, database, symbol, limit, func(_ string, _ int, line string) bool {
		return containsWord(line, symbol)
	})
}

// scanReferences streams files whose full-text index matches symbol and
// collects lines accepted by keep, stopping as soon as limit+1 lines are
// found (the extra one only sets More). Each candidate file's content comes
// back with the match in a single query: the previous implementation built
// an FTS snippet for every candidate, ran a symbol-boost query per candidate,
// and re-fetched each file's content, which could take minutes on common
// identifiers in large repositories. The FTS match (tokenized, stemmed) only
// narrows candidates; keep decides what actually counts on each line.
func scanReferences(ctx context.Context, database *sql.DB, symbol string, limit int, keep func(path string, lineNo int, line string) bool) (*ReferencePage, error) {
	if strings.TrimSpace(symbol) == "" {
		return &ReferencePage{}, nil
	}
	rows, err := database.QueryContext(ctx, `
		SELECT path, content
		FROM file_fts
		WHERE file_fts MATCH ?
		ORDER BY rank;
	`, formatFTSQuery(symbol))
	if err != nil {
		return nil, fmt.Errorf("failed to search references: %w", err)
	}
	defer rows.Close()

	page := &ReferencePage{}
	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			return nil, fmt.Errorf("failed to read reference candidate: %w", err)
		}
		// Cheap pre-check before splitting the file into lines.
		if !strings.Contains(content, symbol) {
			continue
		}
		lineNo := 0
		for rest := content; rest != ""; {
			lineNo++
			line := rest
			if i := strings.IndexByte(rest, '\n'); i >= 0 {
				line, rest = rest[:i], rest[i+1:]
			} else {
				rest = ""
			}
			line = strings.TrimRight(line, "\r")
			if !strings.Contains(line, symbol) || !keep(path, lineNo, line) {
				continue
			}
			if len(page.Refs) == limit {
				page.More = true
				return page, nil
			}
			page.Refs = append(page.Refs, ReferenceResult{
				FilePath:   path,
				LineNumber: lineNo,
				Snippet:    clipAround(strings.TrimSpace(line), symbol, maxSnippetChars),
			})
		}
	}
	return page, rows.Err()
}

// TestLocation represents a test file and its test functions/methods.
type TestLocation struct {
	TestFilePath string   `json:"test_file_path"`
	TestNames    []string `json:"test_names"`
}

// FindTestsFor locates matching test files and their test functions for a given source file or symbol.
func FindTestsFor(ctx context.Context, database *sql.DB, target string) ([]TestLocation, error) {
	allFiles, err := GetAllFiles(ctx, database)
	if err != nil {
		return nil, err
	}

	normTarget := filepath.ToSlash(target)
	baseName := filepath.Base(normTarget)
	ext := filepath.Ext(baseName)
	rawName := strings.TrimSuffix(baseName, ext)

	// Candidate test names: foo_test.go, test_foo.py, foo.test.ts, foo.spec.ts
	candidates := []string{
		rawName + "_test" + ext,
		"test_" + rawName + ext,
		rawName + ".test" + ext,
		rawName + ".spec" + ext,
	}

	var testFiles []string
	for path := range allFiles {
		pNorm := filepath.ToSlash(path)
		for _, cand := range candidates {
			if strings.HasSuffix(pNorm, cand) {
				testFiles = append(testFiles, path)
				break
			}
		}
	}

	// If no direct file match, search by symbol
	if len(testFiles) == 0 {
		testQuery := "Test" + target
		syms, _ := FindSymbols(ctx, database, testQuery)
		seen := make(map[string]bool)
		for _, s := range syms {
			if !seen[s.FilePath] {
				seen[s.FilePath] = true
				testFiles = append(testFiles, s.FilePath)
			}
		}
	}

	var results []TestLocation
	for _, tf := range testFiles {
		syms, _ := FindSymbolsInFile(ctx, database, tf)
		var testNames []string
		for _, s := range syms {
			if strings.HasPrefix(s.Name, "Test") || strings.HasPrefix(s.Name, "test_") || strings.HasPrefix(s.Name, "it(") || strings.HasPrefix(s.Name, "describe(") {
				testNames = append(testNames, fmt.Sprintf("%s (L%d)", s.Name, s.LineNumber))
			}
		}
		results = append(results, TestLocation{
			TestFilePath: tf,
			TestNames:    testNames,
		})
	}

	return results, nil
}

// FindCallees locates symbols declared in the codebase that are referenced inside the given symbol's definition.
func FindCallees(ctx context.Context, database *sql.DB, symbol string) ([]SymbolRecord, error) {
	syms, err := FindSymbols(ctx, database, symbol)
	if err != nil || len(syms) == 0 {
		return nil, fmt.Errorf("symbol '%s' not found", symbol)
	}

	targetSym := syms[0]
	content, err := GetFileContent(ctx, database, targetSym.FilePath)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(content, "\n")
	startLine := targetSym.LineNumber - 1
	if startLine < 0 {
		startLine = 0
	}

	fileSyms, err := FindSymbolsInFile(ctx, database, targetSym.FilePath)
	if err != nil {
		return nil, err
	}

	// Determine the real end of the function body: the line just before the next
	// declared symbol in the same file, instead of a fixed line-count window that
	// silently truncated analysis of any function longer than that window (and
	// could pick up unrelated symbols from whatever code happened to follow it).
	// This mirrors the same conservative next-declaration boundary heuristic
	// GenerateSkeleton already uses.
	endLine := len(lines)
	for _, s := range fileSyms {
		if s.LineNumber > targetSym.LineNumber && s.LineNumber-1 < endLine {
			endLine = s.LineNumber - 1
		}
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if endLine <= startLine {
		endLine = len(lines)
	}

	bodyLines := lines[startLine:endLine]
	// Exclude the target's own declaration/signature line from matching: a
	// receiver type, parameter types, or return type named there (e.g.
	// `func (s *Server) executeTool(...) (*ToolCallResult, error) {`) are not
	// things the function actually calls, and were previously reported as
	// false-positive callees.
	var bodyOnly string
	if len(bodyLines) > 1 {
		bodyOnly = strings.Join(bodyLines[1:], "\n")
	}

	// Look up only the identifiers that actually occur in the body, instead of
	// loading every symbol in the repository (96k+ rows on a large codebase)
	// and substring-testing each one against the body.
	words, calls := bodyIdentifiers(bodyOnly)
	var names []any
	for w := range words {
		if w != symbol && len(w) > 3 {
			names = append(names, w)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Slice(names, func(i, j int) bool { return names[i].(string) < names[j].(string) })

	var callees []SymbolRecord
	seen := make(map[string]bool)
	// Chunk to stay well under SQLite's bound-parameter limit.
	for start := 0; start < len(names); start += 500 {
		chunk := names[start:min(start+500, len(names))]
		rows, err := database.QueryContext(ctx, `
			SELECT file_path, name, kind, signature, line_number
			FROM symbols
			WHERE name IN (?`+strings.Repeat(",?", len(chunk)-1)+`)
			ORDER BY file_path ASC, line_number ASC;
		`, chunk...)
		if err != nil {
			return nil, fmt.Errorf("failed to look up callees: %w", err)
		}
		for rows.Next() {
			var s SymbolRecord
			if err := rows.Scan(&s.FilePath, &s.Name, &s.Kind, &s.Signature, &s.LineNumber); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to scan symbol: %w", err)
			}
			if seen[s.Name] {
				continue
			}
			// A callable symbol (function/method) must appear as an actual call or
			// instantiation — `Name(` — to count; a bare mention elsewhere in the
			// body (e.g. in a comment, or as part of a longer identifier) doesn't
			// mean it's called. Non-callable symbols (types/structs/interfaces)
			// have no equivalent call syntax, so a plain reference still counts —
			// e.g. `Type{...}` composite literals or a variable's declared type.
			if (s.Kind == "function" || s.Kind == "method") && !calls[s.Name] {
				continue
			}
			seen[s.Name] = true
			callees = append(callees, s)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(callees, func(i, j int) bool {
		if callees[i].FilePath != callees[j].FilePath {
			return callees[i].FilePath < callees[j].FilePath
		}
		return callees[i].LineNumber < callees[j].LineNumber
	})
	return callees, nil
}

// bodyIdentifiers returns every whole identifier in body (same boundary rules
// as containsWord) and the subset immediately followed by "(" (containsCall).
func bodyIdentifiers(body string) (words, calls map[string]bool) {
	words, calls = make(map[string]bool), make(map[string]bool)
	for i := 0; i < len(body); {
		if !isIdentByte(body[i]) {
			i++
			continue
		}
		j := i
		for j < len(body) && isIdentByte(body[j]) {
			j++
		}
		w := body[i:j]
		words[w] = true
		if j < len(body) && body[j] == '(' {
			calls[w] = true
		}
		i = j
	}
	return words, calls
}

// BlastRadiusResult represents the impact analysis of changing a symbol.
type BlastRadiusResult struct {
	Symbol     string
	RiskLevel  string // "HIGH", "MEDIUM", "LOW"
	References []ReferenceResult
	// MoreReferences is true when the reference scan hit its cap, so
	// References and AffectedFiles are a lower bound, not the full set.
	MoreReferences bool
	AffectedFiles  []string
	TestsToRun     []string
}

// blastReferenceLimit caps the reference scan behind blast radius analysis.
const blastReferenceLimit = 500

// AnalyzeBlastRadius performs call graph and test suite impact analysis for a
// symbol: it finds every reference, the files they live in, and any test
// suites that already cover it, then derives a coarse risk level from those
// counts. Shared by the CLI `blast` command and the MCP blast_radius tool so
// the two surfaces can't silently drift on what counts as high risk.
func AnalyzeBlastRadius(ctx context.Context, database *sql.DB, symbol string) (*BlastRadiusResult, error) {
	// Strip file prefix if passed like "auth.go:GenerateToken"
	cleanSymbol := symbol
	if strings.Contains(symbol, ":") {
		parts := strings.Split(symbol, ":")
		cleanSymbol = parts[len(parts)-1]
	}

	page, err := FindReferencesPage(ctx, database, cleanSymbol, blastReferenceLimit)
	if err != nil {
		return nil, err
	}
	refs := page.Refs

	fileMap := make(map[string]bool)
	for _, r := range refs {
		fileMap[r.FilePath] = true
	}

	var affectedFiles []string
	for f := range fileMap {
		affectedFiles = append(affectedFiles, f)
	}
	sort.Strings(affectedFiles)

	tests, _ := FindTestsFor(ctx, database, cleanSymbol)
	var testsToRun []string
	for _, t := range tests {
		testsToRun = append(testsToRun, t.TestFilePath)
		for _, name := range t.TestNames {
			testsToRun = append(testsToRun, name)
		}
	}

	riskLevel := "LOW"
	if len(affectedFiles) >= 3 || len(refs) >= 5 {
		riskLevel = "HIGH"
	} else if len(affectedFiles) >= 2 || len(refs) >= 2 {
		riskLevel = "MEDIUM"
	}

	return &BlastRadiusResult{
		Symbol:         cleanSymbol,
		RiskLevel:      riskLevel,
		References:     refs,
		MoreReferences: page.More,
		AffectedFiles:  affectedFiles,
		TestsToRun:     testsToRun,
	}, nil
}

// DecisionRecord represents a durable architectural or design decision recorded by an AI agent.
type DecisionRecord struct {
	ID        int64     `json:"id"`
	Topic     string    `json:"topic"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

// SaveDecision stores an architectural decision in the persistent SQLite database.
func SaveDecision(ctx context.Context, database *sql.DB, topic string, summary string) (*DecisionRecord, error) {
	now := time.Now().UTC()
	res, err := database.ExecContext(ctx, `
		INSERT INTO decisions (topic, summary, created_at)
		VALUES (?, ?, ?);
	`, topic, summary, now)
	if err != nil {
		return nil, fmt.Errorf("failed to save decision: %w", err)
	}

	id, _ := res.LastInsertId()
	return &DecisionRecord{
		ID:        id,
		Topic:     topic,
		Summary:   summary,
		CreatedAt: now,
	}, nil
}

// GetDecisions retrieves stored decisions, optionally filtered by topic.
func GetDecisions(ctx context.Context, database *sql.DB, topic string) ([]DecisionRecord, error) {
	var rows *sql.Rows
	var err error

	if strings.TrimSpace(topic) == "" {
		rows, err = database.QueryContext(ctx, `
			SELECT id, topic, summary, created_at
			FROM decisions
			ORDER BY created_at DESC;
		`)
	} else {
		rows, err = database.QueryContext(ctx, `
			SELECT id, topic, summary, created_at
			FROM decisions
			WHERE topic LIKE ? ESCAPE '\' OR summary LIKE ? ESCAPE '\'
			ORDER BY created_at DESC;
		`, "%"+escapeLike(topic)+"%", "%"+escapeLike(topic)+"%")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query decisions: %w", err)
	}
	defer rows.Close()

	var decisions []DecisionRecord
	for rows.Next() {
		var d DecisionRecord
		if err := rows.Scan(&d.ID, &d.Topic, &d.Summary, &d.CreatedAt); err != nil {
			return nil, err
		}
		decisions = append(decisions, d)
	}

	return decisions, rows.Err()
}

// SavingsReport contains aggregate metrics on time and token efficiency.
type SavingsReport struct {
	TotalQueriesServed    int64   `json:"total_queries_served"`
	TotalTokensSaved      int64   `json:"total_tokens_saved"`
	TotalLatencySavedMs   int64   `json:"total_latency_saved_ms"`
	EstimatedCostSavedUSD float64 `json:"estimated_cost_saved_usd"`
	SpeedMultiplier       float64 `json:"speed_multiplier"`
}

// RecordTelemetry records token and latency savings for an MCP query.
// rawEstimate is the estimated token cost of the raw (non-codive) alternative
// for this call — e.g. reading the whole file — and usedTokens is the actual
// size of the response codive returned. Storing both (rather than just their
// difference) lets GetSavingsReport derive a real speed multiplier from
// aggregate usage instead of guessing a constant.
func RecordTelemetry(ctx context.Context, database *sql.DB, toolName string, usedTokens int, rawEstimate int, latencySavedMs int) {
	if database == nil {
		return
	}
	tokensSaved := rawEstimate - usedTokens
	if tokensSaved < 0 {
		tokensSaved = 0
	}
	_, _ = database.ExecContext(ctx, `
		INSERT INTO telemetry (tool_name, tokens_saved, raw_estimate, latency_saved_ms, created_at)
		VALUES (?, ?, ?, ?, ?);
	`, toolName, tokensSaved, rawEstimate, latencySavedMs, time.Now().UTC())
}

// GetSavingsReport computes aggregate efficiency and cloud cost reduction
// metrics purely from recorded telemetry — it never fabricates data, so a
// fresh install with no recorded queries yet correctly reports all-zero
// stats (SpeedMultiplier reports 1.0, meaning "no measured speedup yet").
func GetSavingsReport(ctx context.Context, database *sql.DB) (*SavingsReport, error) {
	row := database.QueryRowContext(ctx, `
		SELECT
			COUNT(1),
			COALESCE(SUM(tokens_saved), 0),
			COALESCE(SUM(latency_saved_ms), 0),
			COALESCE(SUM(raw_estimate), 0)
		FROM telemetry;
	`)

	var queries int64
	var tokens int64
	var latencyMs int64
	var rawTotal int64
	if err := row.Scan(&queries, &tokens, &latencyMs, &rawTotal); err != nil {
		return nil, fmt.Errorf("failed to compute savings report: %w", err)
	}

	costUSD := (float64(tokens) / 1000000.0) * 3.00 // $3.00 per 1M input tokens

	// Real ratio of raw (uncompressed) token cost to what was actually used,
	// derived from recorded usage rather than a guessed constant. Falls back
	// to 1.0 (no measured speedup) until there's enough data to divide by.
	speedMultiplier := 1.0
	usedTotal := rawTotal - tokens
	if usedTotal > 0 {
		speedMultiplier = float64(rawTotal) / float64(usedTotal)
	}

	return &SavingsReport{
		TotalQueriesServed:    queries,
		TotalTokensSaved:      tokens,
		TotalLatencySavedMs:   latencyMs,
		EstimatedCostSavedUSD: costUSD,
		SpeedMultiplier:       speedMultiplier,
	}, nil
}

// escapeLike escapes LIKE wildcards so a query is matched literally (used with
// ESCAPE '\'). Without it, the '_' in a snake_case name matches any character.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '$' || b >= 0x80 ||
		('0' <= b && b <= '9') || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

// wordMatches calls match for each occurrence of word in s that isn't part of
// a longer identifier, so "Scan" doesn't match inside "ScanIncremental" or
// "rescan". Boundaries are only enforced on edges where word itself starts or
// ends with an identifier character. match receives the index just past the
// occurrence and stops the search by returning true.
func wordMatches(s, word string, match func(end int) bool) bool {
	if word == "" {
		return false
	}
	checkStart := isIdentByte(word[0])
	checkEnd := isIdentByte(word[len(word)-1])
	for from := 0; from <= len(s)-len(word); {
		i := strings.Index(s[from:], word)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(word)
		if (!checkStart || start == 0 || !isIdentByte(s[start-1])) &&
			(!checkEnd || end == len(s) || !isIdentByte(s[end])) && match(end) {
			return true
		}
		from = start + 1
	}
	return false
}

// containsWord reports whether s mentions word as a whole identifier.
func containsWord(s, word string) bool {
	return wordMatches(s, word, func(int) bool { return true })
}

// containsCall reports whether s contains a call to name: the whole
// identifier immediately followed by "(".
func containsCall(s, name string) bool {
	return wordMatches(s, name, func(end int) bool { return end < len(s) && s[end] == '(' })
}

// maxSnippetChars caps a reference snippet. Source lines are normally far
// shorter; this only bites on minified or generated lines, which can be
// hundreds of kilobytes long.
const maxSnippetChars = 240

// clipAround shortens line to about limit bytes, keeping a window centred on
// the first occurrence of word and marking the cut ends with "…".
func clipAround(line, word string, limit int) string {
	if len(line) <= limit {
		return line
	}
	i := strings.Index(line, word)
	if i < 0 {
		i = 0
	}
	start := i + len(word)/2 - limit/2
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > len(line) {
		end = len(line)
		start = max(0, end-limit)
	}
	for start > 0 && !utf8.RuneStart(line[start]) {
		start--
	}
	for end < len(line) && !utf8.RuneStart(line[end]) {
		end++
	}
	out := line[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(line) {
		out += "…"
	}
	return out
}
