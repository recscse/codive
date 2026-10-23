// Package ranker orders read_symbol intent results with a small linear model
// that learns from which results agents actually use.
//
// The model never decides what code exists or where it is: candidates come
// from the symbol index, and the model only reorders them. Each candidate is
// described by a few features (search score, where the query words matched,
// whether it's a test, how often it was useful before); the model scores it
// by a weighted sum. Weights start at ColdStart, which reproduces plain
// word-match ranking, and are retrained from logged queries whose useful
// result is known. A retrained model is adopted only if it ranks held-out
// queries at least as well as the current one.
package ranker

import (
	"math"
	"os"
	"strings"
	"time"
)

// Feature indices into a candidate's feature vector.
const (
	FeatSearchScore = iota // bm25 relative to the best candidate, in (0, 1]
	FeatNameMatch          // fraction of query words found in the name
	FeatDocMatch           // ... in the signature and doc comment
	FeatBodyMatch          // ... in the body
	FeatTestFile           // 1 if the definition is in a test file
	FeatTypeDecl           // 1 if it's a type (class, struct, interface, ...)
	FeatPastUse            // how often it was useful for earlier queries, in [0, 1]
	NumFeatures
)

// FeatureNames label the features in reports, in index order.
var FeatureNames = [NumFeatures]string{
	"search_score", "name_match", "doc_match", "body_match", "test_file", "type_decl", "past_use",
}

// Weights are the model's per-feature weights.
type Weights [NumFeatures]float64

// ColdStart ranks by search score, with tests below similar code. It is the
// model used until enough feedback has been collected, and the baseline every
// retrained model must beat.
var ColdStart = Weights{FeatSearchScore: 1, FeatTestFile: -0.5}

// Score is the model's score for a feature vector; higher ranks first.
func (w Weights) Score(x []float64) float64 {
	s := 0.0
	for i := 0; i < NumFeatures && i < len(x); i++ {
		s += w[i] * x[i]
	}
	return s
}

// Candidate is what Features needs to know about one search result.
type Candidate struct {
	// Score and BestScore are bm25 scores (lower is better, usually
	// negative) of this candidate and of the best candidate for the query.
	Score, BestScore float64
	NameTerms        string
	DocTerms         string
	BodyTerms        string
	IsTest           bool
	IsType           bool
	// Uses is how many earlier queries this definition was useful for.
	Uses int
}

// Features describes c as a feature vector for the query words terms.
func Features(terms []string, c Candidate) []float64 {
	x := make([]float64, NumFeatures)
	if c.BestScore < 0 && c.Score < 0 {
		x[FeatSearchScore] = c.Score / c.BestScore
	}
	x[FeatNameMatch] = matchFraction(terms, c.NameTerms)
	x[FeatDocMatch] = matchFraction(terms, c.DocTerms)
	x[FeatBodyMatch] = matchFraction(terms, c.BodyTerms)
	if c.IsTest {
		x[FeatTestFile] = 1
	}
	if c.IsType {
		x[FeatTypeDecl] = 1
	}
	// 10 earlier uses saturate the feature.
	x[FeatPastUse] = math.Min(math.Log1p(float64(c.Uses))/math.Log1p(10), 1)
	return x
}

// matchFraction is the fraction of terms that appear among words.
func matchFraction(terms []string, words string) float64 {
	if len(terms) == 0 || words == "" {
		return 0
	}
	fields := strings.Fields(words)
	set := make(map[string]bool, len(fields))
	for _, w := range fields {
		set[w] = true
	}
	n := 0
	for _, t := range terms {
		if set[t] {
			n++
			continue
		}
		for _, w := range fields {
			if similarWords(t, w) {
				n++
				break
			}
		}
	}
	return float64(n) / float64(len(terms))
}

// similarWords treats inflections of one word as a match ("fails" and
// "failed", "retry" and "retries"): both are at least 4 letters and share all
// but the last two letters of the shorter one (and at least 4).
func similarWords(a, b string) bool {
	short := min(len(a), len(b))
	if short < 4 {
		return false
	}
	n := max(4, short-2)
	return a[:n] == b[:n]
}

// Example is one logged query whose useful results are known: the feature
// vectors of its candidates in the order they were ranked, how many of them
// were shown to the agent, and which were useful.
type Example struct {
	Candidates [][]float64
	Shown      int
	Positives  []int
}

// Training settings: plain SGD on a pairwise logistic loss, pulled toward
// ColdStart so a little feedback can't swing the model far.
const (
	learningRate   = 0.1
	epochs         = 40
	regularization = 0.01
)

// Train fits weights so that, in each example, every useful candidate scores
// above the shown candidates that weren't useful.
func Train(examples []Example) Weights {
	w := ColdStart
	for epoch := 0; epoch < epochs; epoch++ {
		for _, ex := range examples {
			useful := make(map[int]bool, len(ex.Positives))
			for _, p := range ex.Positives {
				useful[p] = true
			}
			for _, p := range ex.Positives {
				if p < 0 || p >= len(ex.Candidates) {
					continue
				}
				for n := 0; n < min(ex.Shown, len(ex.Candidates)); n++ {
					if useful[n] {
						continue
					}
					var d [NumFeatures]float64
					for i := range d {
						d[i] = ex.Candidates[p][i] - ex.Candidates[n][i]
					}
					// Gradient of log(sigmoid(w·d)) is (1 - sigmoid(w·d)) d.
					g := 1 - sigmoid(w.Score(d[:]))
					for i := range w {
						w[i] += learningRate * (g*d[i] - regularization*(w[i]-ColdStart[i]))
					}
				}
			}
		}
	}
	return w
}

func sigmoid(z float64) float64 { return 1 / (1 + math.Exp(-z)) }

// MRR is the mean reciprocal rank, under w, of the best-ranked useful
// candidate of each example: 1 when it's always first, 0.5 when always
// second.
func MRR(examples []Example, w Weights) float64 {
	if len(examples) == 0 {
		return 0
	}
	total := 0.0
	for _, ex := range examples {
		best := 0
		for _, p := range ex.Positives {
			if p < 0 || p >= len(ex.Candidates) {
				continue
			}
			s := w.Score(ex.Candidates[p])
			rank := 1
			for i, c := range ex.Candidates {
				if i != p && w.Score(c) > s {
					rank++
				}
			}
			if best == 0 || rank < best {
				best = rank
			}
		}
		if best > 0 {
			total += 1 / float64(best)
		}
	}
	return total / float64(len(examples))
}

// Model is the ranker in use, with how it was evaluated.
type Model struct {
	Weights Weights `json:"weights"`
	// Version counts adopted retrains; 0 is ColdStart.
	Version   int       `json:"version"`
	TrainedOn int       `json:"trained_on"`
	UpdatedAt time.Time `json:"updated_at"`
	// HoldoutMRR and BaselineMRR are this model's and ColdStart's MRR on
	// the held-out queries when it was adopted.
	HoldoutMRR  float64 `json:"holdout_mrr"`
	BaselineMRR float64 `json:"baseline_mrr"`
}

// Enabled reports whether intent queries are logged for training and the
// trained model used. CODIVE_LEARNING=off turns both off.
func Enabled() bool {
	return !strings.EqualFold(os.Getenv("CODIVE_LEARNING"), "off")
}

// MetaKey is the index meta key the trained model is stored under, as JSON.
const MetaKey = "intent_ranker"

// DefaultModel is the model before any feedback.
func DefaultModel() Model { return Model{Weights: ColdStart} }

// MinExamples is how many queries with known useful results are needed
// before the model is retrained.
const MinExamples = 20

// Retrain fits a new model on examples (oldest first) and returns it if it
// should replace current: every fourth example is held out, and the new
// model must rank them at least as well as both current and ColdStart.
func Retrain(examples []Example, current Model, now time.Time) (Model, bool) {
	if len(examples) < MinExamples {
		return current, false
	}
	var train, holdout []Example
	for i, ex := range examples {
		if i%4 == 3 {
			holdout = append(holdout, ex)
		} else {
			train = append(train, ex)
		}
	}
	w := Train(train)
	got, cur, base := MRR(holdout, w), MRR(holdout, current.Weights), MRR(holdout, ColdStart)
	if got < cur || got < base {
		return current, false
	}
	return Model{
		Weights:     w,
		Version:     current.Version + 1,
		TrainedOn:   len(train),
		UpdatedAt:   now,
		HoldoutMRR:  got,
		BaselineMRR: base,
	}, true
}
