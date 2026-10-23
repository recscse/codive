package symbols

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/recscse/codive/internal/db"
)

// maxBodyTerms caps the distinct words indexed from one definition's body,
// so a huge generated function can't bloat the search index.
const maxBodyTerms = 400

// maxQueryTerms caps how many words of a search query are used.
const maxQueryTerms = 32

// wordRe matches identifier-like tokens: identifiers in code, and plain
// words in comments and strings.
var wordRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// nonDefinitionKinds are indexed symbols that aren't code definitions (build
// manifests, annotations) and so have no body to show or search.
var nonDefinitionKinds = map[string]bool{
	"annotation": true, "dependency": true, "devDependency": true,
	"package": true, "project": true, "parent_pom": true,
}

// IsDefinition reports whether symbols of kind are code definitions with a
// body, as opposed to build-manifest entries such as dependencies.
func IsDefinition(kind string) bool {
	return !nonDefinitionKinds[kind]
}

// queryStopwords are words that say nothing about what code does. Programming
// words such as "error" or "handle" are kept: they often appear in names.
var queryStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "code": true, "do": true, "does": true, "for": true,
	"from": true, "function": true, "how": true, "in": true, "is": true, "it": true,
	"logic": true, "method": true, "of": true, "on": true, "or": true, "that": true,
	"the": true, "this": true, "to": true, "what": true, "when": true,
	"where": true, "which": true, "with": true,
}

// SplitIdentifier splits an identifier into lowercase words at underscores,
// hyphens, and case changes: "processPaymentError" → [process payment error],
// "HTTPServer" → [http server], "retry_interval" → [retry interval]. Digits
// stay with the word before them ("sha256").
func SplitIdentifier(s string) []string {
	var words []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' || r == '$' }) {
		rs := []rune(part)
		start := 0
		for i := 1; i < len(rs); i++ {
			prev, cur := rs[i-1], rs[i]
			if !unicode.IsUpper(cur) {
				continue
			}
			// A new word starts at an upper-case letter after a lower-case
			// letter or digit, or at the last capital of an acronym that
			// runs into a word ("HTTPServer": the S).
			endsAcronym := unicode.IsUpper(prev) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || endsAcronym {
				words = append(words, strings.ToLower(string(rs[start:i])))
				start = i
			}
		}
		words = append(words, strings.ToLower(string(rs[start:])))
	}
	return words
}

// QueryTerms turns a natural-language search ("where is the retry backoff
// for payment failures?") or an identifier into the distinct lowercase words
// to search for, dropping stopwords and single letters.
func QueryTerms(q string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, tok := range wordRe.FindAllString(q, -1) {
		for _, w := range SplitIdentifier(tok) {
			if len(w) < 2 || queryStopwords[w] || seen[w] {
				continue
			}
			seen[w] = true
			out = append(out, w)
			if len(out) == maxQueryTerms {
				return out
			}
		}
	}
	return out
}

// SearchTerms builds the symbol search entries for one file's symbols: for
// each definition, the words of its name; of its signature and leading doc
// comment; and of its body, comments included. Words inside a body are what
// let "retry backoff" find a function named processPaymentError whose body
// sets retry_interval.
func SearchTerms(language string, content []byte, syms []db.SymbolRecord) []db.SymbolTerms {
	if len(syms) == 0 {
		return nil
	}
	ext := newExtentFinder(language, content)
	decls := make([]int, 0, len(syms))
	for _, s := range syms {
		decls = append(decls, s.LineNumber)
	}
	sort.Ints(decls)

	var out []db.SymbolTerms
	seen := make(map[db.SymbolRecord]bool)
	for _, s := range syms {
		key := db.SymbolRecord{FilePath: s.FilePath, Name: s.Name, Kind: s.Kind, LineNumber: s.LineNumber}
		if !IsDefinition(s.Kind) || seen[key] {
			continue
		}
		seen[key] = true
		t := db.SymbolTerms{
			FilePath:   s.FilePath,
			Name:       s.Name,
			Kind:       s.Kind,
			LineNumber: s.LineNumber,
			NameTerms:  strings.Join(SplitIdentifier(s.Name), " "),
		}
		docWords := wordsIn([]string{s.Signature}, 0)
		start, end, _ := ext.extent(s, nextDecl(decls, s.LineNumber))
		end = max(min(end, len(ext.lines)), s.LineNumber)
		if start > 0 && start <= s.LineNumber {
			docWords = append(docWords, wordsIn(ext.lines[start-1:s.LineNumber-1], 0)...)
			t.BodyTerms = strings.Join(wordsIn(ext.lines[s.LineNumber-1:end], maxBodyTerms), " ")
		}
		t.DocTerms = strings.Join(docWords, " ")
		out = append(out, t)
	}
	return out
}

// nextDecl returns the first declaration line after line in sorted decls,
// or 0 if there is none.
func nextDecl(decls []int, line int) int {
	i := sort.SearchInts(decls, line+1)
	if i < len(decls) {
		return decls[i]
	}
	return 0
}

// wordsIn returns the distinct split words of lines in order of appearance,
// at most limit of them (0 means no limit).
func wordsIn(lines []string, limit int) []string {
	var out []string
	seen := make(map[string]bool)
	for _, l := range lines {
		for _, tok := range wordRe.FindAllString(l, -1) {
			for _, w := range SplitIdentifier(tok) {
				if len(w) < 2 || seen[w] {
					continue
				}
				seen[w] = true
				out = append(out, w)
				if limit > 0 && len(out) == limit {
					return out
				}
			}
		}
	}
	return out
}
