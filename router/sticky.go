package router

import "sync"

// StickyMap maps session IDs to world IDs until destroy/prune.
type StickyMap struct {
	mu   sync.Mutex
	byS  map[string]string // session → world
	byW  map[string]string // world → session (optional reverse)
}

func NewStickyMap() *StickyMap {
	return &StickyMap{
		byS: map[string]string{},
		byW: map[string]string{},
	}
}

// Get returns the world bound to session, if any.
func (m *StickyMap) Get(session string) (world string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	world, ok = m.byS[session]
	return
}

// Bind associates session with world. Overwrites prior binding for that session.
func (m *StickyMap) Bind(session, world string) {
	if session == "" || world == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.byS[session]; ok && old != world {
		delete(m.byW, old)
	}
	if prevSess, ok := m.byW[world]; ok && prevSess != session {
		delete(m.byS, prevSess)
	}
	m.byS[session] = world
	m.byW[world] = session
}

// UnbindWorld drops any sticky session for this world (destroy/prune).
func (m *StickyMap) UnbindWorld(world string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.byW[world]; ok {
		delete(m.byS, sess)
		delete(m.byW, world)
	}
}

// UnbindSession drops a session mapping.
func (m *StickyMap) UnbindSession(session string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if world, ok := m.byS[session]; ok {
		delete(m.byW, world)
		delete(m.byS, session)
	}
}
