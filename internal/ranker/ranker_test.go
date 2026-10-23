package ranker

import (
	"math"
	"testing"
	"time"
)

func TestSimilarWords(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"fails", "failed", true},
		{"retry", "retries", true},
		{"retry", "retried", true},
		{"payment", "payments", true},
		{"pay", "payment", false}, // too short to be sure
		{"process", "processor", true},
		{"charge", "change", false},
		{"interval", "internal", false},
	}
	for _, tt := range tests {
		if got := similarWords(tt.a, tt.b); got != tt.want {
			t.Errorf("similarWords(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestFeatures(t *testing.T) {
	x := Features([]string{"retry", "payment", "fails"}, Candidate{
		Score: -3, BestScore: -6,
		NameTerms: "process payment error",
		DocTerms:  "decides whether failed charge is retried",
		IsTest:    true,
		Uses:      10,
	})
	want := map[int]float64{
		FeatSearchScore: 0.5, FeatNameMatch: 1.0 / 3, FeatDocMatch: 2.0 / 3,
		FeatBodyMatch: 0, FeatTestFile: 1, FeatTypeDecl: 0, FeatPastUse: 1,
	}
	for i, w := range want {
		if math.Abs(x[i]-w) > 1e-9 {
			t.Errorf("%s = %v, want %v", FeatureNames[i], x[i], w)
		}
	}
}

// examples builds n two-candidate queries. In each, candidate 0 has the
// better search score, but the useful one is candidate 1, which was useful
// before (past_use). In holdout positions (i%4 == 3), flip makes the useful
// one candidate 0 instead.
func examples(n int, flipHoldout bool) []Example {
	var out []Example
	for i := 0; i < n; i++ {
		ex := Example{
			Candidates: [][]float64{
				{1, 0.5, 0, 0, 0, 0, 0},
				{0.7, 0.5, 0, 0, 0, 0, 1},
			},
			Shown:     2,
			Positives: []int{1},
		}
		if flipHoldout && i%4 == 3 {
			ex.Positives = []int{0}
		}
		out = append(out, ex)
	}
	return out
}

func TestColdStartRanksBySearchScore(t *testing.T) {
	if got := MRR(examples(8, false), ColdStart); got != 0.5 {
		t.Errorf("cold start should rank the useful candidate second, MRR = %v", got)
	}
}

func TestTrainLearnsWhatIsUseful(t *testing.T) {
	exs := examples(40, false)
	w := Train(exs)
	if got := MRR(exs, w); got != 1 {
		t.Errorf("trained MRR = %v, want 1 (weights %v)", got, w)
	}
	if w[FeatPastUse] <= 0 {
		t.Errorf("past_use weight should become positive, got %v", w[FeatPastUse])
	}
}

func TestRetrain(t *testing.T) {
	now := time.Now()

	if _, ok := Retrain(examples(MinExamples-1, false), DefaultModel(), now); ok {
		t.Error("retrained with fewer than MinExamples queries")
	}

	m, ok := Retrain(examples(40, false), DefaultModel(), now)
	if !ok {
		t.Fatal("a model that ranks held-out queries better should be adopted")
	}
	if m.Version != 1 || m.HoldoutMRR != 1 || m.BaselineMRR != 0.5 || m.TrainedOn != 30 {
		t.Errorf("unexpected model: %+v", m)
	}

	// Held-out queries disagree with the training ones: the trained model
	// ranks them worse than cold start, so it must be rejected.
	if _, ok := Retrain(examples(40, true), DefaultModel(), now); ok {
		t.Error("adopted a model that ranks held-out queries worse than cold start")
	}
}
