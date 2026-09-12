// Package lot is lot-boss: shepherds N Firecracker worlds on a KVM host.
// Phase 2 wires Start/Stop/Exec via runtime/world (jailer+Firecracker v1.15.1).
package lot

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PixnBits/backlot/runtime/world"
)

const (
	DefaultMaxWorlds = 3
	DefaultWarmPool  = 0
)

// Config is host-side paths for the shepherd.
type Config struct {
	MaxWorlds   int
	WarmPool    int
	RequireKVM  bool
	KVMPath     string
	WorkDir     string
	Kernel      string
	Rootfs      string
	Firecracker string
	Jailer      string
	// WaitKVM is jailer --exec-file helper (fc-waitkvm); empty falls back to BACKLOT_FC_WAITKVM.
	WaitKVM string
	DeskURL string
	// RequireJailer: EngineReady is false unless euid==0 (jailer path).
	RequireJailer bool
}

// Boss tracks leased world slots and shepherds Firecracker via runtime/world.
type Boss struct {
	mu       sync.Mutex
	cfg      Config
	worlds   map[string]*slot
	nextCID  uint32
	deskHTTP *http.Client
	stopping bool
}

type slot struct {
	info       World
	w          *world.World
	cancel     context.CancelFunc
	lastActive time.Time
	ttlPause   time.Duration
	paused     bool
}

// World is the JSON shape returned to router/agents.
type World struct {
	ID       string `json:"id"`
	Profile  string `json:"profile"`
	State    string `json:"state"`
	TTLPause string `json:"ttl_pause"`
	TTLStore string `json:"ttl_store"`
	TTLPrune string `json:"ttl_prune"`
	Network  string `json:"network"`
	Engine   string `json:"engine,omitempty"`
	GuestCID uint32 `json:"guest_cid,omitempty"`
	UDS      string `json:"vsock_uds,omitempty"`
	JailRoot string `json:"jail_root,omitempty"`
}

func NewBoss(max, warm int, requireKVM bool) *Boss {
	return NewBossConfig(Config{
		MaxWorlds:  max,
		WarmPool:   warm,
		RequireKVM: requireKVM,
		KVMPath:    "/dev/kvm",
	})
}

func NewBossConfig(cfg Config) *Boss {
	if cfg.MaxWorlds <= 0 {
		cfg.MaxWorlds = DefaultMaxWorlds
	}
	if cfg.WarmPool < 0 {
		cfg.WarmPool = DefaultWarmPool
	}
	if cfg.WarmPool > 1 {
		cfg.WarmPool = 1
	}
	if cfg.KVMPath == "" {
		cfg.KVMPath = "/dev/kvm"
	}
	if cfg.Firecracker == "" {
		cfg.Firecracker = getenv("FIRECRACKER_BIN", "/usr/local/firecracker/v1.15.1/firecracker")
	}
	if cfg.Jailer == "" {
		cfg.Jailer = getenv("JAILER_BIN", "/usr/local/firecracker/v1.15.1/jailer")
	}
	if cfg.WaitKVM == "" {
		cfg.WaitKVM = os.Getenv("BACKLOT_FC_WAITKVM")
	}
	b := &Boss{
		cfg:      cfg,
		worlds:   map[string]*slot{},
		nextCID:  3,
		deskHTTP: &http.Client{Timeout: 10 * time.Second},
	}
	go b.pauseLoop()
	return b
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func (b *Boss) kvmOK() bool {
	if !b.cfg.RequireKVM {
		return true
	}
	f, err := os.OpenFile(b.cfg.KVMPath, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func (b *Boss) artifactsOK() bool {
	for _, p := range []string{b.cfg.Kernel, b.cfg.Rootfs, b.cfg.Firecracker, b.cfg.Jailer} {
		if p == "" {
			return false
		}
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	if b.cfg.RequireJailer {
		if b.cfg.WaitKVM == "" {
			return false
		}
		if _, err := os.Stat(b.cfg.WaitKVM); err != nil {
			return false
		}
	}
	if b.cfg.WorkDir == "" {
		return false
	}
	return true
}

// EngineReady is true when this process can start real worlds.
func (b *Boss) EngineReady() bool {
	if !b.kvmOK() || !b.artifactsOK() {
		return false
	}
	if b.cfg.RequireJailer && os.Geteuid() != 0 {
		return false
	}
	return true
}

func (b *Boss) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		ready := b.EngineReady()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"service":      "lot-boss",
			"engine_ready": ready,
			"max_worlds":   b.cfg.MaxWorlds,
			"warm_pool":    b.cfg.WarmPool,
			"phase":        3,
			"euid":         os.Geteuid(),
		})
	})
	mux.HandleFunc("POST /v1/worlds", b.handleLease)
	mux.HandleFunc("GET /v1/worlds/{id}", b.handleGet)
	mux.HandleFunc("POST /v1/worlds/{id}/heartbeat", b.handleHeartbeat)
	mux.HandleFunc("PUT /v1/worlds/{id}/network", b.handleNetwork)
	mux.HandleFunc("POST /v1/worlds/{id}/exec", b.handleExec)
	mux.HandleFunc("DELETE /v1/worlds/{id}", b.handleDestroy)
	mux.HandleFunc("GET /v1/worlds", b.handleList)
	return mux
}

type leaseBody struct {
	Profile  string `json:"profile"`
	TTLPause string `json:"ttl_pause"`
	TTLStore string `json:"ttl_store"`
	TTLPrune string `json:"ttl_prune"`
}

func (b *Boss) handleList(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]World, 0, len(b.worlds))
	for _, s := range b.worlds {
		out = append(out, s.info)
	}
	writeJSON(w, http.StatusOK, map[string]any{"worlds": out, "live": len(out), "cap": b.cfg.MaxWorlds})
}

func (b *Boss) handleLease(w http.ResponseWriter, r *http.Request) {
	if !b.EngineReady() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "world engine unavailable"})
		return
	}
	var body leaseBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Profile == "" {
		body.Profile = "demo"
	}
	if body.TTLPause == "" {
		body.TTLPause = (15 * time.Minute).String()
	}
	if body.TTLStore == "" {
		body.TTLStore = (2 * time.Hour).String()
	}
	if body.TTLPrune == "" {
		body.TTLPrune = (7 * 24 * time.Hour).String()
	}

	info, err := b.Lease(r.Context(), body)
	if err != nil {
		if strings.Contains(err.Error(), "cap") {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (b *Boss) Lease(ctx context.Context, body leaseBody) (*World, error) {
	b.mu.Lock()
	if b.stopping {
		b.mu.Unlock()
		return nil, fmt.Errorf("lot-boss stopping")
	}
	if len(b.worlds) >= b.cfg.MaxWorlds {
		b.mu.Unlock()
		return nil, fmt.Errorf("cap reached: live=%d max=%d", len(b.worlds), b.cfg.MaxWorlds)
	}
	id, err := newWorldID()
	if err != nil {
		b.mu.Unlock()
		return nil, err
	}
	cid := b.nextCID
	b.nextCID++
	work := filepath.Join(b.cfg.WorkDir, id)
	b.mu.Unlock()

	w, err := world.Start(world.StartOpts{
		ID:          id,
		WorkDir:     work,
		Kernel:      b.cfg.Kernel,
		Rootfs:      b.cfg.Rootfs,
		Firecracker: b.cfg.Firecracker,
		Jailer:      b.cfg.Jailer,
		WaitKVM:     b.cfg.WaitKVM,
		GuestCID:    cid,
	})
	if err != nil {
		return nil, fmt.Errorf("start world: %w", err)
	}

	info := World{
		ID:       id,
		Profile:  body.Profile,
		State:    "running",
		TTLPause: body.TTLPause,
		TTLStore: body.TTLStore,
		TTLPrune: body.TTLPrune,
		Network:  "dark",
		Engine:   w.Engine,
		GuestCID: w.GuestCID,
		UDS:      w.UDS,
		JailRoot: w.JailRoot,
	}
	fwdCtx, cancel := context.WithCancel(context.Background())
	go b.forwardEvents(fwdCtx, id, w.EventsPath)

	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.worlds) >= b.cfg.MaxWorlds {
		cancel()
		w.Stop()
		return nil, fmt.Errorf("cap reached: live=%d max=%d", len(b.worlds), b.cfg.MaxWorlds)
	}
	b.worlds[id] = &slot{info: info, w: w, cancel: cancel, lastActive: time.Now(), ttlPause: parseTTLPause(body.TTLPause)}
	_ = b.commitDesk(id, "world_start", map[string]any{
		"engine":    w.Engine,
		"guest_cid": w.GuestCID,
		"jail_root": w.JailRoot,
	})
	return &info, nil
}

func (b *Boss) handleGet(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.worlds[r.PathValue("id")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "world not found"})
		return
	}
	writeJSON(w, http.StatusOK, s.info)
}

func (b *Boss) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.worlds[r.PathValue("id")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "world not found"})
		return
	}
	s.onActivity()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": s.info.State})
}

func (b *Boss) handleNetwork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phase string `json:"phase"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	info, err := b.SetNetwork(r.PathValue("id"), body.Phase)
	if err != nil {
		if err.Error() == "world not found" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		if err.Error() == "phase must be dark or proxy" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (b *Boss) SetNetwork(id, phase string) (*World, error) {
	switch phase {
	case "dark", "":
		phase = "dark"
	case "proxy":
		phase = "proxy"
	default:
		return nil, fmt.Errorf("phase must be dark or proxy")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.worlds[id]
	if !ok {
		return nil, fmt.Errorf("world not found")
	}
	s.info.Network = phase
	s.onActivity()
	out := s.info
	return &out, nil
}

func (b *Boss) handleExec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body"})
		return
	}
	code, resp, err := b.Exec(r.Context(), id, body)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(resp)
}

type execReq struct {
	Argv    []string `json:"argv"`
	Timeout int      `json:"timeout"`
}

func (b *Boss) Exec(ctx context.Context, id string, body []byte) (int, []byte, error) {
	b.mu.Lock()
	s, ok := b.worlds[id]
	b.mu.Unlock()
	if !ok {
		return 0, nil, fmt.Errorf("world not found")
	}
	b.mu.Lock()
	s.onActivity()
	b.mu.Unlock()
	var req execReq
	if err := json.Unmarshal(body, &req); err != nil {
		return http.StatusBadRequest, mustJSON(map[string]string{"error": "invalid json"}), nil
	}
	if len(req.Argv) == 0 {
		return http.StatusBadRequest, mustJSON(map[string]string{"error": "argv required"}), nil
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30
	}
	res, err := s.w.Exec(ctx, req.Argv, timeout)
	if err != nil {
		return 0, nil, err
	}
	out, _ := json.Marshal(res)
	return http.StatusOK, out, nil
}

func (b *Boss) handleDestroy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := b.Destroy(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "destroyed": true})
}

func (b *Boss) Destroy(id string) error {
	b.mu.Lock()
	s, ok := b.worlds[id]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("world not found")
	}
	delete(b.worlds, id)
	b.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	pid := s.w.CmdPid()
	jail := s.w.JailRoot
	s.w.Stop()
	_ = b.commitDesk(id, "world_stop", map[string]any{"jail_root": jail, "pid": pid})
	return nil
}

// StopAll destroys every live world (orphan cleanup).
func (b *Boss) StopAll() {
	b.mu.Lock()
	b.stopping = true
	ids := make([]string, 0, len(b.worlds))
	for id := range b.worlds {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		_ = b.Destroy(id)
	}
}

// LiveIDs returns current world ids.
func (b *Boss) LiveIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.worlds))
	for id := range b.worlds {
		out = append(out, id)
	}
	return out
}

// SlotMeta returns jail root / pid / cid for tests.
func (b *Boss) SlotMeta(id string) (jailRoot string, pid int, cid uint32, engine string, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.worlds[id]
	if !ok {
		return "", 0, 0, "", false
	}
	return s.w.JailRoot, s.w.CmdPid(), s.w.GuestCID, s.w.Engine, true
}

func (b *Boss) forwardEvents(ctx context.Context, worldID, eventsPath string) {
	var offset int64
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			offset = b.drainEventsFile(worldID, eventsPath, offset)
		}
	}
}

func (b *Boss) drainEventsFile(worldID, path string, offset int64) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return offset
	}
	if st.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return offset
	}
	if len(data) == 0 {
		return offset
	}
	lines := bytes.Split(data, []byte("\n"))
	consumed := len(data)
	// If file doesn't end with newline, hold back the partial line.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if li := bytes.LastIndexByte(data, '\n'); li >= 0 {
			partial := data[li+1:]
			consumed = len(data) - len(partial)
			lines = bytes.Split(data[:li+1], []byte("\n"))
		} else {
			return offset
		}
	}
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		kind, _ := ev["kind"].(string)
		if kind == "" {
			continue
		}
		payload, _ := json.Marshal(ev["payload"])
		utc, _ := ev["utc"].(string)
		_ = b.commitDeskRaw(worldID, kind, payload, utc)
	}
	return offset + int64(consumed)
}

func (b *Boss) commitDesk(worldID, kind string, payload any) error {
	raw, _ := json.Marshal(payload)
	return b.commitDeskRaw(worldID, kind, raw, "")
}

func (b *Boss) commitDeskRaw(worldID, kind string, payload json.RawMessage, utc string) error {
	if b.cfg.DeskURL == "" {
		return nil
	}
	reqBody := map[string]any{
		"world_id": worldID,
		"kind":     kind,
		"payload":  json.RawMessage(payload),
	}
	if utc != "" {
		reqBody["utc"] = utc
	}
	bbody, _ := json.Marshal(reqBody)
	url := strings.TrimRight(b.cfg.DeskURL, "/") + "/v1/events"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bbody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := b.deskHTTP.Do(req)
	if err != nil {
		log.Printf("desk ingest: %v", err)
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 300 {
		log.Printf("desk ingest status %d", res.StatusCode)
	}
	return nil
}

func newWorldID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "w" + hex.EncodeToString(b[:]), nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// CapString documents caps for operators.
func CapString(b *Boss) string {
	return fmt.Sprintf("max_worlds=%d warm_pool=%d", b.cfg.MaxWorlds, b.cfg.WarmPool)
}

// Config returns a copy of the boss config (for tests/main).
func (b *Boss) Config() Config { return b.cfg }
