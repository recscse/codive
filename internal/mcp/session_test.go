package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestSessionTrackerUnit(t *testing.T) {
	st := NewSessionTracker()

	if turn := st.NextTurn(); turn != 1 {
		t.Errorf("expected turn 1, got %d", turn)
	}

	// First time file is seen
	seen, turn := st.CheckAndRecordFile("main.go")
	if seen || turn != 0 {
		t.Errorf("expected seen=false, turn=0, got seen=%v, turn=%d", seen, turn)
	}

	// Turn 2
	st.NextTurn()

	// Second time file is seen
	seen, turn = st.CheckAndRecordFile("main.go")
	if !seen || turn != 1 {
		t.Errorf("expected seen=true, turn=1, got seen=%v, turn=%d", seen, turn)
	}

	compact := st.CompactFileRef("main.go", turn)
	if !strings.Contains(compact, "already delivered in Turn #1") {
		t.Errorf("expected compact ref to mention Turn #1, got: %s", compact)
	}
}

func TestMCPSessionDeduplication(t *testing.T) {
	dir, cleanup := setupTestDB(t)
	defer cleanup()

	s := NewServer(dir, nil, "1.0.0")
	defer s.Close()

	ctx := context.Background()

	// First call to get_file_skeleton for main.go
	res1, err := s.executeTool(ctx, "get_file_skeleton", map[string]any{
		"path": "main.go",
	})
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	text1 := res1.Content[0].Text
	if !strings.Contains(text1, "StartServer") {
		t.Errorf("expected first call to contain full skeleton, got:\n%s", text1)
	}

	// Second call without force=true should return compact deduplicated reference
	res2, err := s.executeTool(ctx, "get_file_skeleton", map[string]any{
		"path": "main.go",
	})
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	text2 := res2.Content[0].Text
	if !strings.Contains(text2, "already delivered in Turn #1") {
		t.Errorf("expected second call to be compact deduplicated reference, got:\n%s", text2)
	}
	if strings.Contains(text2, "StartServer") {
		t.Errorf("expected second call NOT to repeat full skeleton body")
	}

	// Third call with force=true should return the full body again
	res3, err := s.executeTool(ctx, "get_file_skeleton", map[string]any{
		"path":  "main.go",
		"force": true,
	})
	if err != nil {
		t.Fatalf("third call with force failed: %v", err)
	}
	text3 := res3.Content[0].Text
	if !strings.Contains(text3, "StartServer") {
		t.Errorf("expected force=true to return full skeleton, got:\n%s", text3)
	}
}
