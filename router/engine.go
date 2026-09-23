package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LeaseDefaults match PRD demo profile.
const (
	DefaultTTLPause = 15 * time.Minute
	DefaultTTLStore = 2 * time.Hour
	DefaultTTLPrune = 7 * 24 * time.Hour
)

// Engine shepherds Firecracker worlds (lot-boss). Absent → lease must 503.
type Engine interface {
	Available(ctx context.Context) bool
	Lease(ctx context.Context, req LeaseRequest) (*WorldInfo, error)
	Get(ctx context.Context, id string) (*WorldInfo, error)
	Heartbeat(ctx context.Context, id string) error
	Exec(ctx context.Context, id string, body []byte) (int, []byte, error)
	Destroy(ctx context.Context, id string) error
	SetNetwork(ctx context.Context, id, phase string) (*WorldInfo, error)
}

// LeaseRequest is the agent-facing lease body.
type LeaseRequest struct {
	Profile   string `json:"profile"`
	TTLPause  string `json:"ttl_pause,omitempty"`
	TTLStore  string `json:"ttl_store,omitempty"`
	TTLPrune  string `json:"ttl_prune,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// WorldInfo is returned to agents.
type WorldInfo struct {
	ID        string `json:"id"`
	Profile   string `json:"profile,omitempty"`
	State     string `json:"state"`
	TTLPause  string `json:"ttl_pause"`
	TTLStore  string `json:"ttl_store"`
	TTLPrune  string `json:"ttl_prune"`
	Network   string `json:"network,omitempty"`
	Engine    string `json:"engine,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// UnavailableEngine always reports the world engine is down.
type UnavailableEngine struct{}

func (UnavailableEngine) Available(context.Context) bool { return false }

func (UnavailableEngine) Lease(context.Context, LeaseRequest) (*WorldInfo, error) {
	return nil, ErrEngineUnavailable
}

func (UnavailableEngine) Get(context.Context, string) (*WorldInfo, error) {
	return nil, ErrEngineUnavailable
}

func (UnavailableEngine) Heartbeat(context.Context, string) error { return ErrEngineUnavailable }

func (UnavailableEngine) Exec(context.Context, string, []byte) (int, []byte, error) {
	return 0, nil, ErrEngineUnavailable
}

func (UnavailableEngine) Destroy(context.Context, string) error { return ErrEngineUnavailable }

func (UnavailableEngine) SetNetwork(context.Context, string, string) (*WorldInfo, error) {
	return nil, ErrEngineUnavailable
}

// ErrEngineUnavailable is returned when lot-boss/KVM is not present.
var ErrEngineUnavailable = fmt.Errorf("world engine unavailable")

// HTTPEngine talks to lot-boss over HTTP.
type HTTPEngine struct {
	Base   string
	Client *http.Client
}

func NewHTTPEngine(base string) *HTTPEngine {
	return &HTTPEngine{
		Base:   base,
		Client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (e *HTTPEngine) Available(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Base+"/health", nil)
	if err != nil {
		return false
	}
	res, err := e.Client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false
	}
	body, _ := io.ReadAll(res.Body)
	var h struct {
		EngineReady *bool `json:"engine_ready"`
		OK          bool  `json:"ok"`
	}
	if err := json.Unmarshal(body, &h); err != nil {
		// Non-JSON health: treat 200 as available (compat).
		return true
	}
	if h.EngineReady != nil {
		return *h.EngineReady
	}
	return h.OK
}

func (e *HTTPEngine) Lease(ctx context.Context, lr LeaseRequest) (*WorldInfo, error) {
	b, _ := json.Marshal(lr)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Base+"/v1/worlds", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := e.Client.Do(req)
	if err != nil {
		return nil, ErrEngineUnavailable
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusServiceUnavailable {
		return nil, ErrEngineUnavailable
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("lot-boss lease: %s: %s", res.Status, body)
	}
	var info WorldInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (e *HTTPEngine) Get(ctx context.Context, id string) (*WorldInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Base+"/v1/worlds/"+id, nil)
	if err != nil {
		return nil, err
	}
	res, err := e.Client.Do(req)
	if err != nil {
		return nil, ErrEngineUnavailable
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("world not found")
	}
	if res.StatusCode == http.StatusServiceUnavailable {
		return nil, ErrEngineUnavailable
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("lot-boss get: %s", body)
	}
	var info WorldInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (e *HTTPEngine) Heartbeat(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Base+"/v1/worlds/"+id+"/heartbeat", nil)
	if err != nil {
		return err
	}
	res, err := e.Client.Do(req)
	if err != nil {
		return ErrEngineUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusServiceUnavailable {
		return ErrEngineUnavailable
	}
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("heartbeat: %s", b)
	}
	return nil
}

func (e *HTTPEngine) Exec(ctx context.Context, id string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Base+"/v1/worlds/"+id+"/exec", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := e.Client.Do(req)
	if err != nil {
		return 0, nil, ErrEngineUnavailable
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusServiceUnavailable {
		return res.StatusCode, b, ErrEngineUnavailable
	}
	return res.StatusCode, b, nil
}

func (e *HTTPEngine) Destroy(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, e.Base+"/v1/worlds/"+id, nil)
	if err != nil {
		return err
	}
	res, err := e.Client.Do(req)
	if err != nil {
		return ErrEngineUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusServiceUnavailable {
		return ErrEngineUnavailable
	}
	if res.StatusCode >= 300 && res.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("destroy: %s", b)
	}
	return nil
}

func (e *HTTPEngine) SetNetwork(ctx context.Context, id, phase string) (*WorldInfo, error) {
	b, _ := json.Marshal(map[string]string{"phase": phase})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, e.Base+"/v1/worlds/"+id+"/network", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := e.Client.Do(req)
	if err != nil {
		return nil, ErrEngineUnavailable
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusServiceUnavailable {
		return nil, ErrEngineUnavailable
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("world not found")
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("lot-boss network: %s: %s", res.Status, body)
	}
	var info WorldInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ApplyLeaseDefaults fills empty TTL fields.

func ApplyLeaseDefaults(lr *LeaseRequest) {
	if lr.Profile == "" {
		lr.Profile = "demo"
	}
	if lr.TTLPause == "" {
		lr.TTLPause = DefaultTTLPause.String()
	}
	if lr.TTLStore == "" {
		lr.TTLStore = DefaultTTLStore.String()
	}
	if lr.TTLPrune == "" {
		lr.TTLPrune = DefaultTTLPrune.String()
	}
}
