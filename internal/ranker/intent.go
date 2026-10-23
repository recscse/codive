package ranker

import (
	"strings"
	"unicode"
)

// SplitSubwords splits camelCase, PascalCase, snake_case, kebab-case, and dot/slash-separated
// identifiers into normalized lowercase word tokens.
// Examples:
//   - "VerifyCredentials" -> ["verify", "credentials"]
//   - "handleOAuth2Callback" -> ["handle", "oauth", "2", "callback"]
//   - "rate_limiter_v2" -> ["rate", "limiter", "v2"]
func SplitSubwords(s string) []string {
	var words []string
	var current strings.Builder

	runes := []rune(s)
	n := len(runes)
	for i := 0; i < n; i++ {
		r := runes[i]
		if r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '\\' || r == '$' {
			if current.Len() > 0 {
				words = append(words, strings.ToLower(current.String()))
				current.Reset()
			}
			continue
		}

		if unicode.IsDigit(r) {
			if current.Len() > 0 && i > 0 && unicode.IsLetter(runes[i-1]) && strings.ToLower(current.String()) != "v" {
				words = append(words, strings.ToLower(current.String()))
				current.Reset()
			}
		} else if unicode.IsUpper(r) {
			// Transition from lowercase/digit to uppercase (camelCase / PascalCase boundary)
			if current.Len() > 0 && i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				words = append(words, strings.ToLower(current.String()))
				current.Reset()
			} else if current.Len() > 1 && i+1 < n && unicode.IsLower(runes[i+1]) {
				// Transition from acronym sequence to word: "OAUTHCallback" -> "oauth", "callback"
				words = append(words, strings.ToLower(current.String()))
				current.Reset()
			}
		} else if unicode.IsLower(r) {
			// Transition from digit to lowercase letter
			if current.Len() > 0 && i > 0 && unicode.IsDigit(runes[i-1]) {
				words = append(words, strings.ToLower(current.String()))
				current.Reset()
			}
		}

		current.WriteRune(r)
	}

	if current.Len() > 0 {
		words = append(words, strings.ToLower(current.String()))
	}
	return words
}

// IntentSynonyms maps foundational software engineering intent concepts to typical code synonyms.
var IntentSynonyms = map[string][]string{
	"login":        {"auth", "authenticate", "credential", "password", "session", "token", "jwt", "oauth", "verify", "signin"},
	"auth":         {"authenticate", "authorization", "login", "credential", "token", "jwt", "session", "permission", "role", "verify", "identity"},
	"authenticate": {"auth", "login", "credential", "verify", "password", "token", "session"},
	"rate":         {"throttle", "limit", "limiter", "quota", "bucket", "tokenbucket", "leaky", "backoff"},
	"limit":        {"throttle", "rate", "quota", "cap", "max", "boundary"},
	"throttle":     {"rate", "limit", "quota", "backoff", "delay", "cooldown"},
	"database":     {"db", "store", "storage", "repository", "repo", "sql", "query", "persist", "entity", "model"},
	"db":           {"database", "store", "repository", "query", "sql", "sqlite", "postgres", "table"},
	"store":        {"database", "db", "repository", "cache", "persist", "save"},
	"cache":        {"memory", "lru", "ttl", "redis", "memcache", "store", "evict"},
	"config":       {"settings", "configuration", "options", "env", "flags", "properties", "params"},
	"token":        {"jwt", "auth", "session", "key", "secret", "bearer", "ticket", "claims"},
	"test":         {"mock", "stub", "fixture", "fake", "assert", "suite"},
	"route":        {"endpoint", "handler", "controller", "api", "url", "path", "serve"},
	"search":       {"find", "lookup", "query", "filter", "match", "scan", "index"},
	"worker":       {"pool", "queue", "job", "task", "process", "consumer", "dispatcher"},
}

// ExpandTerms expands input query terms with subwords and software domain synonyms.
func ExpandTerms(terms []string) []string {
	seen := make(map[string]bool)
	var expanded []string

	add := func(w string) {
		w = strings.ToLower(strings.TrimSpace(w))
		if w != "" && !seen[w] {
			seen[w] = true
			expanded = append(expanded, w)
		}
	}

	for _, t := range terms {
		add(t)
		subs := SplitSubwords(t)
		for _, sub := range subs {
			add(sub)
			if syns, ok := IntentSynonyms[sub]; ok {
				for _, syn := range syns {
					add(syn)
				}
			}
		}
	}
	return expanded
}

// TrigramSimilarity computes the character 3-gram Dice coefficient (0.0 to 1.0)
// between two strings in pure Go.
func TrigramSimilarity(a, b string) float64 {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == b {
		return 1.0
	}
	if len(a) < 3 || len(b) < 3 {
		if strings.Contains(a, b) || strings.Contains(b, a) {
			return 0.75
		}
		return 0.0
	}

	triA := make(map[string]int)
	for i := 0; i <= len(a)-3; i++ {
		triA[a[i:i+3]]++
	}

	triB := make(map[string]int)
	for i := 0; i <= len(b)-3; i++ {
		triB[b[i:i+3]]++
	}

	intersection := 0
	for k, countA := range triA {
		if countB, ok := triB[k]; ok {
			if countA < countB {
				intersection += countA
			} else {
				intersection += countB
			}
		}
	}

	total := (len(a) - 2) + (len(b) - 2)
	return float64(2*intersection) / float64(total)
}
