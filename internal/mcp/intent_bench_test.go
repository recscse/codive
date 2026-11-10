package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/recscse/codive/internal/db"
)

func TestBenchQuery(t *testing.T) {
	tests := []struct{ subject, query, skip string }{
		{"fix(mcp): retry declined payment charges", "retry declined payment charges", ""},
		{"Add backoff to the payment retry", "Add backoff to the payment retry", ""},
		{"docs: explain retries", "", "docs/chore/test/release commit"},
		{"chore(release): v1.2.0", "", "docs/chore/test/release commit"},
		{"Merge pull request #4 from x/y", "", "merge or revert"},
		{"fix: typo", "", "subject too short to search"},
	}
	for _, tt := range tests {
		q, skip := benchQuery(tt.subject)
		if q != tt.query || skip != tt.skip {
			t.Errorf("benchQuery(%q) = %q, %q; want %q, %q", tt.subject, q, skip, tt.query, tt.skip)
		}
	}
}

func TestParseNewSideHunks(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -3,0 +4,2 @@ func x\n+a\n+b\n@@ -10 +12 @@\n-x\n+y\n@@ -20,3 +21,0 @@\n" +
		"diff --git a/gone.go b/gone.go\n--- a/gone.go\n+++ /dev/null\n@@ -1,5 +0,0 @@\n"
	got := parseNewSideHunks([]byte(diff))
	want := map[string][][2]int{"a.go": {{4, 5}, {12, 12}, {21, 21}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseNewSideHunks = %v, want %v", got, want)
	}
}

// End to end on a throwaway repository: each commit's subject must find the
// function it changed.
func TestRunIntentBenchmark(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "pay.go"), []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("package pay\n\nfunc chargeCard() {}\n\nfunc sendReceipt() {}\n")
	git("add", ".")
	git("commit", "-q", "-m", "initial")
	write("package pay\n\nfunc chargeCard() {\n\t// charge the card\n}\n\nfunc sendReceipt() {}\n")
	git("commit", "-qam", "fix: charge card twice")
	write("package pay\n\nfunc chargeCard() {\n\t// charge the card\n}\n\nfunc sendReceipt() {\n\t// email receipt\n}\n")
	git("commit", "-qam", "send receipt by email")
	git("commit", "-q", "--allow-empty", "-m", "docs: readme")

	indexForTest(t, dir)
	server := NewServer(dir, nil, "test")
	defer server.Close()
	database, _, err := server.getDBForPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := RunIntentBenchmark(context.Background(), database, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if rep.CommitsScanned != 4 || len(rep.Cases) != 2 {
		t.Fatalf("want 4 commits scanned, 2 used; got %d, %d (skipped %v)", rep.CommitsScanned, len(rep.Cases), rep.Skipped)
	}
	for _, c := range rep.Cases {
		if c.SymbolRank != 1 || len(c.Answers) != 1 {
			t.Errorf("%q: answers %v, rank %d, top %v", c.Query, c.Answers, c.SymbolRank, c.Top)
		}
	}
	if rep.Symbol.Hit1 != 1 {
		t.Errorf("hit@1 = %v, want 1", rep.Symbol.Hit1)
	}
	if stats, _ := db.GetLearningStats(context.Background(), database); stats.Logged != 0 {
		t.Errorf("benchmark logged %d intent queries; it must not feed the learner", stats.Logged)
	}
}
