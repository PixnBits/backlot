package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type memEngine struct {
	mu     sync.Mutex
	worlds map[string]*WorldInfo
	next   int
}

func newMemEngine() *memEngine {
	return &memEngine{worlds: map[string]*WorldInfo{}}
}

func (e *memEngine) Available(context.Context) bool { return true }

func (e *memEngine) Lease(_ context.Context, req LeaseRequest) (*WorldInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.next++
	id := "w-" + string(rune('0'+e.next))
	if e.next > 9 {
		id = "w-x"
	}
	info := &WorldInfo{
		ID:       id,
		Profile:  req.Profile,
		State:    "running",
		TTLPause: req.TTLPause,
		TTLStore: req.TTLStore,
		TTLPrune: req.TTLPrune,
		Network:  "dark",
		Engine:   "mock",
	}
	// stable ids for stickiness tests
	id = "world-" + req.Profile
	if req.Profile == "" {
		id = "world-demo"
	}
	info.ID = id
	e.worlds[id] = info
	cp := *info
	return &cp, nil
}

func (e *memEngine) Get(_ context.Context, id string) (*WorldInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	w, ok := e.worlds[id]
	if !ok {
		return nil, ErrEngineUnavailable
	}
	cp := *w
	return &cp, nil
}

func (e *memEngine) Heartbeat(context.Context, string) error { return nil }
func (e *memEngine) Exec(context.Context, string, []byte) (int, []byte, error) {
	return 200, []byte(`{"ok":true}`), nil
}
func (e *memEngine) Destroy(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.worlds, id)
	return nil
}

func TestLeaseWithoutEngineIs503(t *testing.T) {
	srv := NewServer(UnavailableEngine{}, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/worlds", bytes.NewReader([]byte(`{"profile":"demo"}`)))
	rr := httptest.NewRecorder()
	srv.Mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["error"] != "world engine unavailable" {
		t.Fatalf("body=%v", body)
	}
}

func TestHealthOK(t *testing.T) {
	srv := NewServer(UnavailableEngine{}, "")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	srv.Mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestStickyLeaseReturnsSameWorld(t *testing.T) {
	eng := newMemEngine()
	srv := NewServer(eng, "")
	mk := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/worlds", bytes.NewReader([]byte(`{"profile":"demo"}`)))
		req.Header.Set("X-Session-Id", "agent-1")
		rr := httptest.NewRecorder()
		srv.Mux.ServeHTTP(rr, req)
		return rr
	}
	rr1 := mk()
	if rr1.Code != http.StatusCreated && rr1.Code != http.StatusOK {
		t.Fatalf("first lease status=%d body=%s", rr1.Code, rr1.Body.String())
	}
	var w1 WorldInfo
	_ = json.Unmarshal(rr1.Body.Bytes(), &w1)
	rr2 := mk()
	if rr2.Code != http.StatusOK {
		t.Fatalf("sticky lease status=%d body=%s", rr2.Code, rr2.Body.String())
	}
	var w2 WorldInfo
	_ = json.Unmarshal(rr2.Body.Bytes(), &w2)
	if w1.ID == "" || w1.ID != w2.ID {
		t.Fatalf("sticky mismatch %q vs %q", w1.ID, w2.ID)
	}
}

func TestSnapshot501(t *testing.T) {
	srv := NewServer(UnavailableEngine{}, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/worlds/x/snapshot", nil)
	rr := httptest.NewRecorder()
	srv.Mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d", rr.Code)
	}
}
