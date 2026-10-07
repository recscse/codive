package db

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestIntentLearningLog(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), ".codive", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	now := time.Now()

	cands := []IntentCandidate{
		{FilePath: "pay.go", Name: "RetryCharge", Kind: "function", LineNumber: 3, Features: []float64{1, 1}},
		{FilePath: "pay.go", Name: "nextAttempt", Kind: "function", LineNumber: 9, Features: []float64{0.7, 0}},
	}
	id1, err := LogIntent(ctx, database, "retry charge", cands, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := LogIntent(ctx, database, "charge again", cands, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LogIntent(ctx, database, "never resolved", cands, 2, now); err != nil {
		t.Fatal(err)
	}

	if err := ResolveIntent(ctx, database, id1, []int{1}, now); err != nil {
		t.Fatal(err)
	}
	if err := ResolveIntent(ctx, database, id2, nil, now); err != nil {
		t.Fatal(err)
	}

	labelled, err := LabelledIntents(ctx, database, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(labelled) != 1 || labelled[0].ID != id1 || !reflect.DeepEqual(labelled[0].Positives, []int{1}) ||
		labelled[0].Shown != 2 || !reflect.DeepEqual(labelled[0].Candidates, cands) {
		t.Fatalf("unexpected labelled queries: %+v", labelled)
	}
	if n := SymbolUses(ctx, database, "pay.go", "nextAttempt", "function"); n != 1 {
		t.Errorf("nextAttempt uses = %d, want 1", n)
	}
	if n := SymbolUses(ctx, database, "pay.go", "RetryCharge", "function"); n != 0 {
		t.Errorf("RetryCharge uses = %d, want 0", n)
	}

	stats, err := GetLearningStats(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if stats != (LearningStats{Logged: 3, Resolved: 2, Labelled: 1, UsedSyms: 1, TotalUses: 1}) {
		t.Errorf("unexpected stats: %+v", stats)
	}

	// Feedback survives a full index rebuild...
	if err := ApplyIndexChanges(ctx, database, IndexChanges{ReplaceAll: true}); err != nil {
		t.Fatal(err)
	}
	if stats, _ := GetLearningStats(ctx, database); stats.Logged != 3 {
		t.Errorf("rebuild dropped the intent log: %+v", stats)
	}

	// ...but not a reset.
	if err := SetMeta(ctx, database, "intent_ranker", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := ResetLearning(ctx, database, "intent_ranker"); err != nil {
		t.Fatal(err)
	}
	if stats, _ := GetLearningStats(ctx, database); stats != (LearningStats{}) {
		t.Errorf("reset left data behind: %+v", stats)
	}
	if v, _ := GetMeta(ctx, database, "intent_ranker"); v != "" {
		t.Errorf("reset left the model behind: %q", v)
	}
}
