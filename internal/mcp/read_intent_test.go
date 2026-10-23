package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// intentFixture indexes a small repository where the code that implements
// "exponential backoff for payment failures" never uses those words in its
// name: processPaymentError doubles retryInterval.
func intentFixture(t *testing.T) (string, func(args map[string]any) string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"payments/gateway.go": `package payments

import "time"

// Gateway talks to the card processor.
type Gateway struct {
	retryInterval time.Duration
}

// processPaymentError decides whether a failed charge is tried again.
func (g *Gateway) processPaymentError(err error, attempt int) time.Duration {
	if attempt > 5 {
		return 0
	}
	g.retryInterval *= 2
	return g.retryInterval
}

// Charge sends a charge to the processor.
func (g *Gateway) Charge(amount int) error { return nil }
`,
		"payments/gateway_test.go": `package payments

import "testing"

func TestProcessPaymentErrorRetryInterval(t *testing.T) {
	g := &Gateway{retryInterval: 1}
	g.processPaymentError(nil, 1)
}
`,
		"notify/email.go": `package notify

// Send delivers an email. It does not retry.
func Send(to string) error { return nil }
`,
	}
	for name, src := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}
	indexForTest(t, dir)
	server := NewServer(dir, nil, "test")
	t.Cleanup(server.Close)
	return dir, func(args map[string]any) string {
		t.Helper()
		args["workspace_path"] = dir
		res, err := server.executeTool(context.Background(), "read_symbol", args)
		if err != nil {
			t.Fatalf("read_symbol(%v) failed: %v", args, err)
		}
		return res.Content[0].Text
	}
}

// firstDefinition returns the header line of the first definition shown.
func firstDefinition(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "### ") {
			return l
		}
	}
	return ""
}

func TestReadSymbolIntentFindsCodeByWhatItDoes(t *testing.T) {
	_, call := intentFixture(t)
	out := call(map[string]any{"intent": "retry backoff interval payment error"})

	if h := firstDefinition(out); !strings.Contains(h, "`processPaymentError`") {
		t.Fatalf("best match should be processPaymentError, got %q:\n%s", h, out)
	}
	if !strings.Contains(out, "g.retryInterval *= 2") {
		t.Errorf("missing the definition's body:\n%s", out)
	}
	if strings.Contains(out, "Charge(amount int) error { return nil }") {
		t.Errorf("returned code outside the definition:\n%s", out)
	}
	// The test that mentions the same words is listed, but ranked below.
	others := out[strings.Index(out, "Other candidates"):]
	if !strings.Contains(others, "TestProcessPaymentErrorRetryInterval") {
		t.Errorf("runner-up test should be listed as a candidate:\n%s", out)
	}
	if strings.Count(out, "### ") != 1 {
		t.Errorf("intent should show one definition in full by default:\n%s", out)
	}
}

func TestReadSymbolIntentEntryPoints(t *testing.T) {
	_, call := intentFixture(t)

	// A "symbol" that is really a description is treated as an intent.
	out := call(map[string]any{"symbol": "retry backoff interval payment error"})
	if !strings.Contains(firstDefinition(out), "`processPaymentError`") {
		t.Errorf("spaced symbol should search by intent:\n%s", out)
	}

	// A guessed name that doesn't exist falls back to the intent.
	out = call(map[string]any{"symbol": "computeBackoff", "intent": "retry interval payment"})
	if !strings.Contains(firstDefinition(out), "`processPaymentError`") {
		t.Errorf("missing name should fall back to intent:\n%s", out)
	}

	// path narrows the search to a directory.
	out = call(map[string]any{"intent": "send email retry", "path": "payments"})
	if strings.Contains(out, "notify/email.go") {
		t.Errorf("path filter ignored:\n%s", out)
	}

	out = call(map[string]any{"intent": "kubernetes helm chart"})
	if !strings.Contains(out, "No definitions mention any of: kubernetes helm chart") {
		t.Errorf("unexpected no-match message:\n%s", out)
	}
}

// Edits made after indexing must be reflected in intent results too.
func TestReadSymbolIntentIsFresh(t *testing.T) {
	dir, call := intentFixture(t)
	path := filepath.Join(dir, "payments", "gateway.go")
	src, _ := os.ReadFile(path)
	updated := strings.Replace(string(src), "g.retryInterval *= 2", "g.retryInterval *= 3 // freshly edited", 1)
	if err := os.WriteFile(path, []byte("\n\n\n"+updated), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	_ = os.Chtimes(path, future, future)

	out := call(map[string]any{"intent": "retry backoff interval payment error"})
	h := firstDefinition(out)
	if !strings.Contains(h, "`processPaymentError`") || !strings.Contains(h, "L13-20") || !strings.Contains(out, "freshly edited") {
		t.Errorf("edit not reflected (expected new body at L13-20), header %q:\n%s", h, out)
	}
}
