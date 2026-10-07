package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchSymbolsFollowsFileChanges(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), ".codive", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	now := time.Now()
	file := func(p string) FileRecord {
		return FileRecord{Path: p, Language: "Go", ContentHash: p, LastModified: now, LastIndexed: now}
	}
	sym := func(p, name string, line int) SymbolRecord {
		return SymbolRecord{FilePath: p, Name: name, Kind: "function", Signature: "func " + name + "()", LineNumber: line}
	}

	if err := ApplyIndexChanges(ctx, database, IndexChanges{
		Files:   []FileRecord{file("pay.go"), file("mail.go")},
		Symbols: []SymbolRecord{sym("pay.go", "processPaymentError", 3), sym("mail.go", "Send", 5)},
		SymbolTerms: []SymbolTerms{
			{FilePath: "pay.go", Name: "processPaymentError", Kind: "function", LineNumber: 3,
				NameTerms: "process payment error", BodyTerms: "retry interval backoff"},
			{FilePath: "mail.go", Name: "Send", Kind: "function", LineNumber: 5,
				NameTerms: "send", DocTerms: "send email no retry"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	hits, err := SearchSymbols(ctx, database, []string{"retry", "backoff", "payment"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Name != "processPaymentError" || hits[0].Signature != "func processPaymentError()" {
		t.Fatalf("want processPaymentError first (with its signature), then Send; got %+v", hits)
	}
	if hits[0].Score >= hits[1].Score {
		t.Errorf("scores not ordered best-first: %+v", hits)
	}

	// Re-indexing a file replaces its entries; deleting one removes them.
	if err := ApplyIndexChanges(ctx, database, IndexChanges{
		Files:   []FileRecord{file("pay.go")},
		Symbols: []SymbolRecord{sym("pay.go", "chargeCard", 3)},
		SymbolTerms: []SymbolTerms{{FilePath: "pay.go", Name: "chargeCard", Kind: "function", LineNumber: 3,
			NameTerms: "charge card"}},
		Deleted: []string{"mail.go"},
	}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := SearchSymbols(ctx, database, []string{"retry", "backoff", "payment"}, 10); len(hits) != 0 {
		t.Errorf("stale entries survived re-index/delete: %+v", hits)
	}
	if hits, _ := SearchSymbols(ctx, database, []string{"card"}, 10); len(hits) != 1 || hits[0].Name != "chargeCard" {
		t.Errorf("new entry not searchable: %+v", hits)
	}

	// A quote inside a term must not break the FTS query syntax.
	if _, err := SearchSymbols(ctx, database, []string{`we"ird`}, 10); err != nil {
		t.Errorf("quote in term broke the query: %v", err)
	}
}
