package symbols

import (
	"reflect"
	"strings"
	"testing"

	"github.com/recscse/codive/internal/db"
)

func TestSplitIdentifier(t *testing.T) {
	tests := map[string][]string{
		"processPaymentError": {"process", "payment", "error"},
		"HTTPServer":          {"http", "server"},
		"parseJSON":           {"parse", "json"},
		"retry_interval":      {"retry", "interval"},
		"MAX_RETRIES":         {"max", "retries"},
		"sha256Sum":           {"sha256", "sum"},
		"x":                   {"x"},
		"kebab-case-name":     {"kebab", "case", "name"},
	}
	for in, want := range tests {
		if got := SplitIdentifier(in); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitIdentifier(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestQueryTerms(t *testing.T) {
	got := QueryTerms("Where is the logic that handles exponential backoff when a payment gateway fails? retryInterval, retry")
	want := []string{"handles", "exponential", "backoff", "payment", "gateway", "fails", "retry", "interval"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("QueryTerms = %v, want %v", got, want)
	}
	if got := QueryTerms("the of a ?"); len(got) != 0 {
		t.Errorf("stopwords only should give no terms, got %v", got)
	}
}

func TestSearchTerms(t *testing.T) {
	src := `package payments

// Gateway talks to the card processor.
type Gateway struct {
	retryInterval int
}

// processPaymentError decides whether a failed charge is tried again.
func (g *Gateway) processPaymentError(err error) int {
	g.retryInterval *= 2 // exponential backoff
	return g.retryInterval
}

func (g *Gateway) Charge(amount int) error { return nil }
`
	syms, err := ExtractSymbols("payments/gateway.go", "Go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	syms = append(syms, db.SymbolRecord{FilePath: "payments/gateway.go", Name: "fmt", Kind: "dependency", LineNumber: 1})
	terms := SearchTerms("Go", []byte(src), syms)

	byName := make(map[string]db.SymbolTerms)
	for _, tm := range terms {
		byName[tm.Name] = tm
	}
	if _, ok := byName["fmt"]; ok {
		t.Error("non-definition kinds must not be indexed for search")
	}
	ppe, ok := byName["processPaymentError"]
	if !ok {
		t.Fatalf("processPaymentError not indexed: %+v", terms)
	}
	if ppe.NameTerms != "process payment error" {
		t.Errorf("NameTerms = %q", ppe.NameTerms)
	}
	for _, w := range []string{"decides", "failed", "gateway"} {
		if !strings.Contains(" "+ppe.DocTerms+" ", " "+w+" ") {
			t.Errorf("DocTerms %q missing %q", ppe.DocTerms, w)
		}
	}
	for _, w := range []string{"retry", "interval", "exponential", "backoff"} {
		if !strings.Contains(" "+ppe.BodyTerms+" ", " "+w+" ") {
			t.Errorf("BodyTerms %q missing %q", ppe.BodyTerms, w)
		}
	}
	// The body ends where the definition does: Charge's words aren't in it.
	if strings.Contains(ppe.BodyTerms, "amount") {
		t.Errorf("BodyTerms leaked into the next definition: %q", ppe.BodyTerms)
	}
	if gw := byName["Gateway"]; !strings.Contains(gw.DocTerms, "card processor") {
		t.Errorf("type doc comment not indexed: %+v", gw)
	}
}

func TestSearchTermsCapsBody(t *testing.T) {
	var b strings.Builder
	b.WriteString("package big\n\nfunc Huge() {\n")
	for i := 0; i < maxBodyTerms+100; i++ {
		b.WriteString("\tword" + strings.Repeat("x", i%7) + "Item" + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + "()\n")
	}
	b.WriteString("}\n")
	syms, _ := ExtractSymbols("big.go", "Go", []byte(b.String()))
	terms := SearchTerms("Go", []byte(b.String()), syms)
	if len(terms) != 1 {
		t.Fatalf("expected one entry, got %d", len(terms))
	}
	if n := len(strings.Fields(terms[0].BodyTerms)); n > maxBodyTerms {
		t.Errorf("body has %d terms, cap is %d", n, maxBodyTerms)
	}
}
