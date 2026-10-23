package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// maxIntentLog bounds the intent log: older queries are dropped as new ones
// are logged.
const maxIntentLog = 5000

// IntentCandidate is one ranked result of a logged intent query, with the
// ranking features it had when the query was answered.
type IntentCandidate struct {
	FilePath   string    `json:"path"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	LineNumber int       `json:"line"`
	Features   []float64 `json:"f"`
}

// IntentRecord is a logged intent query.
type IntentRecord struct {
	ID         int64
	CreatedAt  time.Time
	Query      string
	Candidates []IntentCandidate
	// Shown is how many of the first candidates the agent was shown.
	Shown int
	// Positives are the indexes of the candidates that proved useful.
	Positives []int
}

// LogIntent records an answered intent query and returns its id.
func LogIntent(ctx context.Context, database *sql.DB, query string, cands []IntentCandidate, shown int, at time.Time) (int64, error) {
	data, err := json.Marshal(cands)
	if err != nil {
		return 0, err
	}
	res, err := database.ExecContext(ctx, `
		INSERT INTO intent_log (created_at, query, candidates, shown) VALUES (?, ?, ?, ?);
	`, at.UTC(), query, string(data), shown)
	if err != nil {
		return 0, fmt.Errorf("failed to log intent query: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, _ = database.ExecContext(ctx, "DELETE FROM intent_log WHERE id <= ?;", id-maxIntentLog)
	return id, nil
}

// ResolveIntent records which candidates of a logged query proved useful
// (possibly none) and counts a use for each of them.
func ResolveIntent(ctx context.Context, database *sql.DB, id int64, positives []int, at time.Time) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT candidates FROM intent_log WHERE id = ?;", id).Scan(&raw); err != nil {
		return fmt.Errorf("intent query %d: %w", id, err)
	}
	var cands []IntentCandidate
	if err := json.Unmarshal([]byte(raw), &cands); err != nil {
		return fmt.Errorf("intent query %d: %w", id, err)
	}
	if positives == nil {
		positives = []int{}
	}
	data, _ := json.Marshal(positives)
	if _, err := tx.ExecContext(ctx, "UPDATE intent_log SET positives = ?, resolved_at = ? WHERE id = ?;", string(data), at.UTC(), id); err != nil {
		return err
	}
	for _, p := range positives {
		if p < 0 || p >= len(cands) {
			continue
		}
		c := cands[p]
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO symbol_usage (file_path, name, kind, uses, last_used) VALUES (?, ?, ?, 1, ?)
			ON CONFLICT(file_path, name, kind) DO UPDATE SET uses = uses + 1, last_used = excluded.last_used;
		`, c.FilePath, c.Name, c.Kind, at.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LabelledIntents returns the most recent limit resolved queries that had at
// least one useful candidate, oldest first.
func LabelledIntents(ctx context.Context, database *sql.DB, limit int) ([]IntentRecord, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, created_at, query, candidates, shown, positives FROM (
			SELECT * FROM intent_log
			WHERE positives IS NOT NULL AND positives != '[]'
			ORDER BY id DESC LIMIT ?
		) ORDER BY id ASC;
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to read intent log: %w", err)
	}
	defer rows.Close()
	var out []IntentRecord
	for rows.Next() {
		var r IntentRecord
		var created, cands, pos string
		if err := rows.Scan(&r.ID, &created, &r.Query, &cands, &r.Shown, &pos); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTimestamp(created)
		if json.Unmarshal([]byte(cands), &r.Candidates) != nil || json.Unmarshal([]byte(pos), &r.Positives) != nil {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SymbolUses returns how many logged queries the definition was useful for.
func SymbolUses(ctx context.Context, database *sql.DB, filePath, name, kind string) int {
	var n int
	_ = database.QueryRowContext(ctx, "SELECT uses FROM symbol_usage WHERE file_path = ? AND name = ? AND kind = ?;",
		filePath, name, kind).Scan(&n)
	return n
}

// LearningStats summarizes the feedback collected for intent ranking.
type LearningStats struct {
	Logged    int `json:"logged_queries"`
	Resolved  int `json:"resolved_queries"`
	Labelled  int `json:"queries_with_useful_result"`
	UsedSyms  int `json:"definitions_used"`
	TotalUses int `json:"total_uses"`
}

// GetLearningStats counts the logged queries and recorded uses.
func GetLearningStats(ctx context.Context, database *sql.DB) (LearningStats, error) {
	var s LearningStats
	err := database.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(positives IS NOT NULL), 0),
		       COALESCE(SUM(positives IS NOT NULL AND positives != '[]'), 0)
		FROM intent_log;
	`).Scan(&s.Logged, &s.Resolved, &s.Labelled)
	if err != nil {
		return s, err
	}
	err = database.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(uses), 0) FROM symbol_usage;").Scan(&s.UsedSyms, &s.TotalUses)
	return s, err
}

// ResetLearning forgets all collected feedback and the trained ranker
// stored under modelKey in meta.
func ResetLearning(ctx context.Context, database *sql.DB, modelKey string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{"DELETE FROM intent_log;", "DELETE FROM symbol_usage;"} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM meta WHERE key = ?;", modelKey); err != nil {
		return err
	}
	return tx.Commit()
}
