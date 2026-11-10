package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
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

// History training counts only code definitions as useful: commits that
// changed nothing but a test give it no example.
func TestLearnFromHistory(t *testing.T) {
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
	names := []string{"charge", "refund", "invoice", "receipt", "ledger"}
	render := func(rev int) (code, test string) {
		code, test = "package pay\n", "package pay\n\nimport \"testing\"\n"
		for _, n := range names {
			code += "\nfunc " + n + "Payment() {\n\t_ = " + strconv.Itoa(rev) + "\n}\n"
			test += "\nfunc Test" + n + "Payment(t *testing.T) {\n\t_ = " + strconv.Itoa(rev) + "\n}\n"
		}
		return
	}
	write := func(code, test string) {
		t.Helper()
		_ = os.WriteFile(filepath.Join(dir, "pay.go"), []byte(code), 0644)
		_ = os.WriteFile(filepath.Join(dir, "pay_test.go"), []byte(test), 0644)
	}
	git("init", "-q")
	code, test := render(0)
	write(code, test)
	git("add", ".")
	git("commit", "-q", "-m", "initial")
	for i := 1; i <= 30; i++ {
		n := names[i%len(names)]
		code = strings.Replace(code, "func "+n+"Payment() {\n\t_ = ", "func "+n+"Payment() {\n\t_ = 1+", 1)
		test = strings.Replace(test, "func Test"+n+"Payment(t *testing.T) {\n\t_ = ", "func Test"+n+"Payment(t *testing.T) {\n\t_ = 1+", 1)
		write(code, test)
		msg := "fix " + n + " payment rounding"
		if i > 25 {
			git("add", "pay_test.go")
			git("checkout", "--", "pay.go")
			git("commit", "-q", "-m", "cover "+n+" payment rounding")
			b, _ := os.ReadFile(filepath.Join(dir, "pay.go"))
			code = string(b)
			continue
		}
		git("commit", "-qam", msg)
	}

	indexForTest(t, dir)
	server := NewServer(dir, nil, "test")
	defer server.Close()
	database, _, err := server.getDBForPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, trained, adopted, n, err := LearnFromHistory(context.Background(), database, dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	if n != 25 {
		t.Errorf("want 25 examples (the 5 test-only commits give none), got %d", n)
	}
	stored := loadRanker(context.Background(), database)
	if adopted != (stored.Version == 1) || (adopted && stored.Weights != trained.Weights) {
		t.Errorf("adopted=%v but stored model is %+v", adopted, stored)
	}
}
