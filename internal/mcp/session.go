package mcp

import (
	"fmt"
	"sync"
)

// SessionTracker tracks files and symbols delivered to an AI assistant during
// an active MCP connection to eliminate duplicate context in multi-turn dialogues.
type SessionTracker struct {
	mu          sync.RWMutex
	turn        int
	seenFiles   map[string]int // relPath -> turn first delivered
	seenSymbols map[string]int // symbol key (e.g. "path:name") -> turn first delivered
}

// NewSessionTracker initializes a new thread-safe session tracker.
func NewSessionTracker() *SessionTracker {
	return &SessionTracker{
		seenFiles:   make(map[string]int),
		seenSymbols: make(map[string]int),
	}
}

// NextTurn increments and returns the active conversation turn counter.
func (st *SessionTracker) NextTurn() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.turn++
	return st.turn
}

// CurrentTurn returns the current conversation turn counter.
func (st *SessionTracker) CurrentTurn() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.turn
}

// CheckAndRecordFile returns true and the turn number if the file skeleton was already delivered.
func (st *SessionTracker) CheckAndRecordFile(relPath string) (bool, int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if t, exists := st.seenFiles[relPath]; exists {
		return true, t
	}
	st.seenFiles[relPath] = st.turn
	return false, 0
}

// CheckAndRecordSymbol returns true and the turn number if the symbol was already delivered.
func (st *SessionTracker) CheckAndRecordSymbol(symKey string) (bool, int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if t, exists := st.seenSymbols[symKey]; exists {
		return true, t
	}
	st.seenSymbols[symKey] = st.turn
	return false, 0
}

// CompactSymbolRef returns a token-saving reference string for an already-delivered symbol.
func (st *SessionTracker) CompactSymbolRef(name, filePath string, line int, firstTurn int) string {
	return fmt.Sprintf("// [Symbol '%s' already delivered in Turn #%d at %s:%d — pass force=true to reprint full body]\n",
		name, firstTurn, filePath, line)
}

// CompactFileRef returns a token-saving reference string for an already-delivered file skeleton.
func (st *SessionTracker) CompactFileRef(filePath string, firstTurn int) string {
	return fmt.Sprintf("// [File skeleton for '%s' already delivered in Turn #%d — pass force=true to reprint full skeleton]\n",
		filePath, firstTurn)
}

// Reset clears session history.
func (st *SessionTracker) Reset() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.turn = 0
	st.seenFiles = make(map[string]int)
	st.seenSymbols = make(map[string]int)
}
