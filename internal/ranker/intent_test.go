package ranker

import (
	"reflect"
	"testing"
)

func TestSplitSubwords(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"VerifyCredentials", []string{"verify", "credentials"}},
		{"handleOAuth2Callback", []string{"handle", "oauth", "2", "callback"}},
		{"rate_limiter_v2", []string{"rate", "limiter", "v2"}},
		{"DBConnectionPool", []string{"db", "connection", "pool"}},
		{"user-auth-token", []string{"user", "auth", "token"}},
		{"pkg/auth/login.go", []string{"pkg", "auth", "login", "go"}},
	}

	for _, tt := range tests {
		got := SplitSubwords(tt.input)
		if !reflect.DeepEqual(got, tt.expected) {
			t.Errorf("SplitSubwords(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestExpandTerms(t *testing.T) {
	terms := []string{"login"}
	expanded := ExpandTerms(terms)

	// Verify that essential synonyms are included
	required := []string{"auth", "authenticate", "credential", "session", "token", "verify"}
	expandedMap := make(map[string]bool)
	for _, term := range expanded {
		expandedMap[term] = true
	}

	for _, req := range required {
		if !expandedMap[req] {
			t.Errorf("ExpandTerms(%v) missing expected synonym %q", terms, req)
		}
	}
}

func TestTrigramSimilarity(t *testing.T) {
	// Exact match
	if score := TrigramSimilarity("throttle", "throttle"); score != 1.0 {
		t.Errorf("expected 1.0, got %f", score)
	}

	// Morphological variation
	score := TrigramSimilarity("throttling", "throttle")
	if score < 0.6 {
		t.Errorf("expected high similarity for throttling/throttle, got %f", score)
	}

	// Completely unrelated
	scoreUnrelated := TrigramSimilarity("authenticate", "database")
	if scoreUnrelated > 0.3 {
		t.Errorf("expected low similarity for authenticate/database, got %f", scoreUnrelated)
	}
}
