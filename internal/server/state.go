package server

import "sync"

// State is the shared, mutex-guarded build state for the currently targeted
// presentation file.
type State struct {
	mu      sync.RWMutex
	gen     int    // bumped on every file switch (independent of version)
	src     string // absolute path to current file, "" if none loaded
	root    string // absolute Typst root for current src, "" if none loaded
	version int
	errMsg  string
	pdf     []byte
	notes   []string
}

func NewState() *State {
	return &State{}
}

func (s *State) SetError(msg string) {
	s.mu.Lock()
	s.errMsg = msg
	s.mu.Unlock()
}

func (s *State) SetDeck(pdf []byte, notes []string) {
	s.mu.Lock()
	s.errMsg, s.pdf, s.notes = "", pdf, notes
	s.version++
	s.mu.Unlock()
}

// SetTarget records a newly picked file/root and bumps gen, clearing stale
// build output from the previous file.
func (s *State) SetTarget(src, root string) {
	s.mu.Lock()
	s.src, s.root = src, root
	s.gen++
	s.errMsg, s.pdf, s.notes = "", nil, nil
	s.mu.Unlock()
}

func (s *State) Target() (src, root string, gen int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.src, s.root, s.gen
}
