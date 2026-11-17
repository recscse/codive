package mcp

import (
	"strings"
	"testing"
)

func hitLines(hits []lineHit) []int {
	var out []int
	for _, h := range hits {
		out = append(out, h.line)
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMatchingLines(t *testing.T) {
	content := "# Tasks\n" + // 1
		"TIQ-100 NEEDS-HUMAN client data\n" + // 2
		"TIQ-101 TODO depends on 100\n" + // 3
		"TIQ-102 todo depends on 101\n" + // 4
		"\n" + // 5
		"retry with   backoff here\n" + // 6
		"backoff then retry\n" + // 7
		"only retry\n" // 8

	cases := []struct {
		name   string
		words  []string
		phrase bool
		want   []int
	}{
		{"every occurrence, any case", []string{"TODO"}, false, []int{3, 4}},
		{"all words on one line", []string{"retry", "backoff"}, false, []int{6, 7}},
		{"words spread over lines fall back to any word", []string{"tasks", "needs-human"}, false, []int{1, 2}},
		{"phrase keeps word order", []string{"retry", "with", "backoff"}, true, []int{6}},
		{"phrase that never appears", []string{"backoff", "with", "retry"}, true, nil},
	}
	for _, tc := range cases {
		if got := hitLines(matchingLines(content, tc.words, tc.phrase)); !sameInts(got, tc.want) {
			t.Errorf("%s: lines %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestClipLineKeepsTheMatchVisible(t *testing.T) {
	text := strings.Repeat("a", 1000) + "NEEDLE" + strings.Repeat("b", 1000)
	got := clipLine(text, 1000)
	if !strings.Contains(got, "NEEDLE") {
		t.Errorf("match clipped away: %q", got)
	}
	if len(got) > maxSearchLineChars+8 {
		t.Errorf("clipped line is %d bytes, want about %d", len(got), maxSearchLineChars)
	}
	if short := "short line"; clipLine(short, 0) != short {
		t.Error("a short line was changed")
	}
}

func TestTokenFooterClaimsNoSavingWithoutABaseline(t *testing.T) {
	if got := tokenFooter(300, 0); strings.Contains(got, "smaller") || strings.Contains(got, "Saved") {
		t.Errorf("footer claims a saving with no baseline: %q", got)
	}
	if got := tokenFooter(250, 1000); !strings.Contains(got, "Whole file: ~1000 tokens (75% smaller)") {
		t.Errorf("footer with a measured baseline: %q", got)
	}
	if got := tokenFooter(1200, 1000); strings.Contains(got, "smaller") {
		t.Errorf("footer claims a saving for a response larger than the file: %q", got)
	}
}
