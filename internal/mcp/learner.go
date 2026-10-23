package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/ranker"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/symbols"
)

const (
	// intentFeedbackWindow is how long after an intent query the agent's
	// follow-up calls and edits count as feedback on its results.
	intentFeedbackWindow = 15 * time.Minute
	// maxTrainingQueries bounds how many recent labelled queries a retrain
	// uses.
	maxTrainingQueries = 2000
)

// intentCand is a logged candidate as the learner tracks it.
type intentCand struct {
	path, name, kind string
	// start and end are the definition's lines and bodyHash its text when
	// it was shown; all zero for candidates that weren't shown.
	start, end int
	bodyHash   string
}

// pendingIntent is an answered intent query still collecting feedback.
type pendingIntent struct {
	id        int64
	dir       string
	at        time.Time
	cands     []intentCand
	positives map[int]bool
}

// learner tracks recent intent queries and which of their results the agent
// went on to use: reading one by name, asking for its callers, reading its
// lines, or editing its body. Once a query's feedback window closes, the
// used results are recorded and the ranker is retrained.
type learner struct {
	mu      sync.Mutex
	pending []*pendingIntent
	now     func() time.Time // for tests; nil means time.Now
}

func (l *learner) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

func (l *learner) add(p *pendingIntent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending = append(l.pending, p)
}

// observe marks the candidates that a tool call about to run in workspace
// dir is using.
func (l *learner) observe(tool string, args map[string]any, dir string) {
	match := usageMatcher(tool, args)
	if match == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	for _, p := range l.pending {
		if p.dir != dir || now.Sub(p.at) > intentFeedbackWindow {
			continue
		}
		for i, c := range p.cands {
			if match(c) {
				p.positives[i] = true
			}
		}
	}
}

// usageMatcher returns which candidates a tool call uses, or nil if the call
// says nothing about any.
func usageMatcher(tool string, args map[string]any) func(intentCand) bool {
	str := func(k string) string { v, _ := args[k].(string); return strings.TrimSpace(v) }
	switch tool {
	case "read_symbol":
		raw := str("symbol")
		if raw == "" || strings.ContainsAny(raw, " \t") {
			return nil // an intent query, not a use
		}
		sq := parseSymbolQuery(raw, str("path"))
		return func(c intentCand) bool {
			return c.name == sq.name && (sq.path == "" || pathMatches(c.path, sq.path))
		}
	case "find_callers", "find_references", "blast_radius":
		name := parseSymbolQuery(str("symbol"), "").name
		if name == "" {
			return nil
		}
		return func(c intentCand) bool { return c.name == name }
	case "find_tests_for":
		target := str("target")
		if target == "" {
			return nil
		}
		name := parseSymbolQuery(target, "").name
		return func(c intentCand) bool { return c.name == name || pathMatches(c.path, target) }
	case "read_file_context":
		path := str("path")
		start, _ := args["start_line"].(float64)
		end, _ := args["end_line"].(float64)
		if path == "" || start <= 0 {
			return nil // a whole-file read doesn't single out a definition
		}
		if end < start {
			end = start
		}
		return func(c intentCand) bool {
			return c.start > 0 && pathMatches(c.path, path) && int(start) <= c.end && int(end) >= c.start
		}
	}
	return nil
}

// pathMatches reports whether the indexed path file is the one filter names.
func pathMatches(file, filter string) bool {
	filter = strings.TrimPrefix(filepath.ToSlash(filter), "./")
	return file == filter || strings.HasSuffix(file, "/"+filter)
}

// due removes and returns the queries whose feedback window has closed, or
// all of them when flush is set.
func (l *learner) due(flush bool) []*pendingIntent {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	var out, keep []*pendingIntent
	for _, p := range l.pending {
		if flush || now.Sub(p.at) > intentFeedbackWindow {
			out = append(out, p)
		} else {
			keep = append(keep, p)
		}
	}
	l.pending = keep
	return out
}

// resolveIntents records the feedback of every query whose window has closed
// (all pending ones when flush is set), then retrains the ranker of each
// workspace that got new feedback.
func (s *Server) resolveIntents(ctx context.Context, flush bool) {
	retrain := make(map[*sql.DB]bool)
	for _, p := range s.learn.due(flush) {
		database, _, err := s.getDBForPath(p.dir)
		if err != nil {
			continue
		}
		markEdited(p)
		positives := make([]int, 0, len(p.positives))
		for i := range p.positives {
			positives = append(positives, i)
		}
		sort.Ints(positives)
		if db.ResolveIntent(ctx, database, p.id, positives, s.learn.clock()) == nil && len(positives) > 0 {
			retrain[database] = true
		}
	}
	for database := range retrain {
		s.retrainRanker(ctx, database)
	}
}

// markEdited marks the shown candidates whose body was edited during the
// query's feedback window.
func markEdited(p *pendingIntent) {
	deadline := p.at.Add(intentFeedbackWindow)
	files := newFileCache(p.dir)
	for i, c := range p.cands {
		if c.bodyHash == "" || p.positives[i] {
			continue
		}
		full, err := validateSafeRelPath(p.dir, c.path)
		if err != nil {
			continue
		}
		info, err := os.Stat(full)
		if err != nil || !info.ModTime().After(p.at) || info.ModTime().After(deadline) {
			continue
		}
		f, err := files.get(c.path)
		if err != nil {
			continue
		}
		// The definition may have moved: compare against the same-named one
		// closest to where it was.
		var cur *db.SymbolRecord
		for j := range f.syms {
			sym := &f.syms[j]
			if sym.Name == c.name && sym.Kind == c.kind && (cur == nil || absDiff(sym.LineNumber, c.start) < absDiff(cur.LineNumber, c.start)) {
				cur = sym
			}
		}
		if cur == nil {
			continue
		}
		if _, _, hash := definitionText(f, *cur); hash != "" && hash != c.bodyHash {
			p.positives[i] = true
		}
	}
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// definitionText returns the line range of sym in f and a hash of its text.
func definitionText(f *cachedFile, sym db.SymbolRecord) (start, end int, hash string) {
	start, end, _ = symbols.SymbolExtent(f.lang, f.content, sym, nextDeclLine(f.syms, sym.LineNumber))
	if start < 1 || end > len(f.lines) {
		return 0, 0, ""
	}
	return start, end, scanner.HashBytes([]byte(strings.Join(f.lines[start-1:end], "\n")))
}

// loadRanker returns the workspace's trained ranker, or the cold-start one.
func loadRanker(ctx context.Context, database *sql.DB) ranker.Model {
	if !ranker.Enabled() {
		return ranker.DefaultModel()
	}
	raw, err := db.GetMeta(ctx, database, ranker.MetaKey)
	if err != nil || raw == "" {
		return ranker.DefaultModel()
	}
	var m ranker.Model
	if json.Unmarshal([]byte(raw), &m) != nil {
		return ranker.DefaultModel()
	}
	return m
}

// retrainRanker refits the ranker on the workspace's labelled queries and
// stores it if it ranks held-out queries at least as well as before.
func (s *Server) retrainRanker(ctx context.Context, database *sql.DB) {
	records, err := db.LabelledIntents(ctx, database, maxTrainingQueries)
	if err != nil || len(records) < ranker.MinExamples {
		return
	}
	examples := make([]ranker.Example, 0, len(records))
	for _, r := range records {
		ex := ranker.Example{Shown: r.Shown, Positives: r.Positives}
		for _, c := range r.Candidates {
			ex.Candidates = append(ex.Candidates, c.Features)
		}
		examples = append(examples, ex)
	}
	m, adopted := ranker.Retrain(examples, loadRanker(ctx, database), s.learn.clock())
	if !adopted {
		return
	}
	if data, err := json.Marshal(m); err == nil {
		_ = db.SetMeta(ctx, database, ranker.MetaKey, string(data))
	}
}
