package mcp

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/recscse/codive/internal/db"
)

// learnFixture indexes files and returns the server, its fake clock, and a
// tool caller. The clock starts at the real time so file mtimes line up.
func learnFixture(t *testing.T, files map[string]string) (string, *Server, *time.Time, func(tool string, args map[string]any) string) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}
	indexForTest(t, dir)
	server := NewServer(dir, nil, "test")
	t.Cleanup(server.Close)
	clock := time.Now()
	server.learn.now = func() time.Time { return clock }
	return dir, server, &clock, func(tool string, args map[string]any) string {
		t.Helper()
		args["workspace_path"] = dir
		res, err := server.executeTool(context.Background(), tool, args)
		if err != nil {
			t.Fatalf("%s(%v) failed: %v", tool, args, err)
		}
		return res.Content[0].Text
	}
}

func learnDB(t *testing.T, s *Server, dir string) *sql.DB {
	t.Helper()
	database, _, err := s.getDBForPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

const billingSrc = `package billing

// RetryCharge retries a charge.
func RetryCharge() {}

// scheduleNextAttempt works out when a declined charge is retried.
func scheduleNextAttempt() {}
`

// When the agent keeps choosing the second-ranked result, the ranker learns
// to put it first.
func TestIntentRankingLearnsFromUse(t *testing.T) {
	dir, server, clock, call := learnFixture(t, map[string]string{"billing/retry.go": billingSrc})

	if h := firstDefinition(call("read_symbol", map[string]any{"intent": "retry charge"})); !strings.Contains(h, "`RetryCharge`") {
		t.Fatalf("cold start should rank RetryCharge first (name match), got %q", h)
	}
	*clock = clock.Add(intentFeedbackWindow + time.Minute)

	for i := 0; i < 26; i++ {
		call("read_symbol", map[string]any{"intent": "retry charge"})
		call("read_symbol", map[string]any{"symbol": "billing/retry.go:scheduleNextAttempt"})
		*clock = clock.Add(intentFeedbackWindow + time.Minute)
	}
	out := call("read_symbol", map[string]any{"intent": "retry charge"})
	if h := firstDefinition(out); !strings.Contains(h, "`scheduleNextAttempt`") {
		t.Fatalf("after repeated use scheduleNextAttempt should rank first, got %q:\n%s", h, out)
	}

	database := learnDB(t, server, dir)
	m := loadRanker(context.Background(), database)
	if m.Version == 0 || m.HoldoutMRR <= m.BaselineMRR {
		t.Errorf("expected an adopted model that beats the baseline, got %+v", m)
	}
	if n := db.SymbolUses(context.Background(), database, "billing/retry.go", "scheduleNextAttempt", "function"); n != 26 {
		t.Errorf("scheduleNextAttempt uses = %d, want 26", n)
	}
	if n := db.SymbolUses(context.Background(), database, "billing/retry.go", "RetryCharge", "function"); n != 0 {
		t.Errorf("RetryCharge was never used, got %d uses", n)
	}
}

// Editing a shown definition within the feedback window marks it useful;
// later calls don't count.
func TestIntentFeedbackFromEditsAndCalls(t *testing.T) {
	dir, server, clock, call := learnFixture(t, map[string]string{"billing/retry.go": billingSrc})
	ctx := context.Background()
	start := *clock

	call("read_symbol", map[string]any{"intent": "retry charge"})
	path := filepath.Join(dir, "billing", "retry.go")
	edited := strings.Replace(billingSrc, "func scheduleNextAttempt() {}", "func scheduleNextAttempt() {\n\t// backoff\n}", 1)
	if err := os.WriteFile(path, []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	editTime := start.Add(time.Minute)
	_ = os.Chtimes(path, editTime, editTime)

	// A read after the window closes is not feedback.
	*clock = start.Add(intentFeedbackWindow + time.Minute)
	call("read_symbol", map[string]any{"symbol": "RetryCharge"})

	database := learnDB(t, server, dir)
	if n := db.SymbolUses(ctx, database, "billing/retry.go", "scheduleNextAttempt", "function"); n != 1 {
		t.Errorf("edited definition should count as used, got %d", n)
	}
	if n := db.SymbolUses(ctx, database, "billing/retry.go", "RetryCharge", "function"); n != 0 {
		t.Errorf("read after the window should not count, got %d", n)
	}

	// A line-range read overlapping a shown definition counts; pending
	// feedback is recorded when the server closes.
	out := call("read_symbol", map[string]any{"intent": "retry charge"})
	if !strings.Contains(out, "RetryCharge") {
		t.Fatalf("unexpected result:\n%s", out)
	}
	call("read_file_context", map[string]any{"path": "billing/retry.go", "start_line": float64(3), "end_line": float64(4)})
	server.resolveIntents(ctx, true)
	if n := db.SymbolUses(ctx, database, "billing/retry.go", "RetryCharge", "function"); n != 1 {
		t.Errorf("line-range read should count as used, got %d", n)
	}
}

func TestIntentLearningCanBeTurnedOff(t *testing.T) {
	t.Setenv("CODIVE_LEARNING", "off")
	dir, server, _, call := learnFixture(t, map[string]string{"billing/retry.go": billingSrc})
	call("read_symbol", map[string]any{"intent": "retry charge"})
	stats, err := db.GetLearningStats(context.Background(), learnDB(t, server, dir))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Logged != 0 {
		t.Errorf("CODIVE_LEARNING=off still logged %d queries", stats.Logged)
	}
}
