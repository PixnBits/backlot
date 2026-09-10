package router

import "testing"

func TestSessionStickiness(t *testing.T) {
	m := NewStickyMap()
	m.Bind("sess-a", "world-1")
	got, ok := m.Get("sess-a")
	if !ok || got != "world-1" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	// Same session stays sticky.
	m.Bind("sess-a", "world-1")
	got, ok = m.Get("sess-a")
	if !ok || got != "world-1" {
		t.Fatalf("rebind same: %q", got)
	}
	// Destroy/prune clears sticky.
	m.UnbindWorld("world-1")
	if _, ok := m.Get("sess-a"); ok {
		t.Fatal("expected unbound after destroy")
	}
	// Rebind after prune.
	m.Bind("sess-a", "world-2")
	got, ok = m.Get("sess-a")
	if !ok || got != "world-2" {
		t.Fatalf("after rebind: %q", got)
	}
	// Session move unbinds prior world reverse map.
	m.Bind("sess-b", "world-2")
	if _, ok := m.Get("sess-a"); ok {
		t.Fatal("sess-a should lose world-2 when sess-b takes it")
	}
	got, ok = m.Get("sess-b")
	if !ok || got != "world-2" {
		t.Fatalf("sess-b: %q", got)
	}
}
