// Package lot is lot-boss: shepherds N Firecracker worlds on a KVM host.
// Phase 1 ships a thin HTTP stub. Real Start/Stop/Exec via runtime/world is Phase 2.
package lot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
)

const (
	DefaultMaxWorlds = 3
	DefaultWarmPool  = 0
)

// Boss tracks leased world slots. Without KVM/runtime wiring it refuses leases.
type Boss struct {
	mu         sync.Mutex
	MaxWorlds  int
	WarmPool   int
	RequireKVM bool
	KVMPath    string
	worlds     map[string]*World
}

type World struct {
	ID       string `json:"id"`
	Profile  string `json:"profile"`
	State    string `json:"state"`
	TTLPause string `json:"ttl_pause"`
	TTLStore string `json:"ttl_store"`
	TTLPrune string `json:"ttl_prune"`
	Network  string `json:"network"`
	Engine   string `json:"engine,omitempty"`
}

func NewBoss(max, warm int, requireKVM bool) *Boss {
	if max <= 0 {
		max = DefaultMaxWorlds
	}
	if warm < 0 {
		warm = DefaultWarmPool
	}
	if warm > 1 {
		warm = 1 // Phase 1/2: warm pool 0 or 1
	}
	return &Boss{
		MaxWorlds:  max,
		WarmPool:   warm,
		RequireKVM: requireKVM,
		KVMPath:    "/dev/kvm",
		worlds:     map[string]*World{},
	}
}

func (b *Boss) kvmOK() bool {
	if !b.RequireKVM {
		return true
	}
	f, err := os.OpenFile(b.KVMPath, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// EngineReady is true only when this process can actually start worlds.
// Phase 1 stub: always false so Compose CP without host lot-boss never invents worlds.
// Phase 2 will flip this when runtime/world Start is wired and KVM is present.
func (b *Boss) EngineReady() bool {
	return false
}

func (b *Boss) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		ready := b.EngineReady() && b.kvmOK()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"service":       "lot-boss",
			"engine_ready":  ready,
			"max_worlds":    b.MaxWorlds,
			"warm_pool":     b.WarmPool,
			"phase":         1,
		})
	})
	mux.HandleFunc("POST /v1/worlds", b.handleLease)
	mux.HandleFunc("GET /v1/worlds/{id}", b.handleGet)
	mux.HandleFunc("POST /v1/worlds/{id}/heartbeat", b.handleHeartbeat)
	mux.HandleFunc("POST /v1/worlds/{id}/exec", b.handleExec)
	mux.HandleFunc("DELETE /v1/worlds/{id}", b.handleDestroy)
	return mux
}

func (b *Boss) handleLease(w http.ResponseWriter, r *http.Request) {
	if !b.EngineReady() || !b.kvmOK() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "world engine unavailable"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "world engine unavailable"})
}

func (b *Boss) handleGet(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	world, ok := b.worlds[r.PathValue("id")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "world not found"})
		return
	}
	writeJSON(w, http.StatusOK, world)
}

func (b *Boss) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.worlds[r.PathValue("id")]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "world not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (b *Boss) handleExec(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "world engine unavailable"})
}

func (b *Boss) handleDestroy(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.worlds, r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// CapString documents the Phase 1 caps for operators.
func CapString(b *Boss) string {
	return fmt.Sprintf("max_worlds=%d warm_pool=%d", b.MaxWorlds, b.WarmPool)
}
