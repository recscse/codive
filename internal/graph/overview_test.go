package graph

import (
	"context"
	"fmt"
	"testing"

	"github.com/recscse/codive/internal/db"
)

func TestRepoAreasDescendsPastWrapperFolders(t *testing.T) {
	files := map[string]db.FileRecord{}
	// Everything sits under src/main/java/com/acme, then splits into modules.
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("src/main/java/com/acme/billing/B%d.java", i)] = db.FileRecord{}
		files[fmt.Sprintf("src/main/java/com/acme/orders/O%d.java", i)] = db.FileRecord{}
	}
	areas := repoAreas(files, map[string]int{"src/main/java/com/acme/billing/B0.java": 7})
	if len(areas) != 2 {
		t.Fatalf("want 2 areas, got %+v", areas)
	}
	var billing *AreaStat
	for i := range areas {
		if areas[i].Path == "src/main/java/com/acme/billing" {
			billing = &areas[i]
		}
	}
	if billing == nil || billing.Files != 10 || billing.Symbols != 7 {
		t.Fatalf("billing area wrong: %+v", areas)
	}
}

func TestRepoAreasKeepsBalancedTopLevel(t *testing.T) {
	files := map[string]db.FileRecord{"a/x.go": {}, "a/y.go": {}, "b/z.go": {}, "main.go": {}}
	areas := repoAreas(files, nil)
	if len(areas) != 3 { // a, b, (root)
		t.Fatalf("want 3 areas, got %+v", areas)
	}
}

func TestSearchNodesEscapesWildcardsAndRanksExactFirst(t *testing.T) {
	database, err := db.Open(t.TempDir() + "/i.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	nodes := []Node{
		{ID: "a", Kind: KindFunction, Label: "payment_total", FilePath: "x.go"},
		{ID: "b", Kind: KindFunction, Label: "pay", FilePath: "x.go"},
		{ID: "c", Kind: KindFunction, Label: "payXment", FilePath: "x.go"},
	}
	if err := saveGraphToDB(context.Background(), database, nodes, nil); err != nil {
		t.Fatal(err)
	}

	got, err := SearchNodes(context.Background(), database, "pay", 10)
	if err != nil || len(got) != 3 || got[0].Label != "pay" {
		t.Fatalf("exact match should rank first: %+v err=%v", got, err)
	}
	// "_" is a LIKE wildcard. Escaped it is a literal underscore, which
	// appears in none of these names right after "pay".
	got, err = SearchNodes(context.Background(), database, "pay_", 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("wildcard leaked: %+v err=%v", got, err)
	}
}

func TestNodeNeighborhoodReturnsLinksAndOtherEnds(t *testing.T) {
	database, err := db.Open(t.TempDir() + "/i.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	nodes := []Node{
		{ID: "f1", Kind: KindFunction, Label: "caller", FilePath: "a.go"},
		{ID: "f2", Kind: KindFunction, Label: "callee", FilePath: "a.go"},
		{ID: "f3", Kind: KindFunction, Label: "unrelated", FilePath: "a.go"},
	}
	edges := []Edge{{SourceID: "f1", TargetID: "f2", Kind: EdgeCalls, Evidence: EvidenceDirect, Confidence: 1}}
	if err := saveGraphToDB(context.Background(), database, nodes, edges); err != nil {
		t.Fatal(err)
	}
	snap, err := NodeNeighborhood(context.Background(), database, "f2")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Nodes) != 2 || len(snap.Edges) != 1 {
		t.Fatalf("want callee + caller and one edge, got %d nodes %d edges", len(snap.Nodes), len(snap.Edges))
	}
	if _, err := NodeNeighborhood(context.Background(), database, "missing"); err == nil {
		t.Fatal("unknown node should be an error")
	}
}
