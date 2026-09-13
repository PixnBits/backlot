package router

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Server is the public agent-facing HTTP facade.
type Server struct {
	Engine  Engine
	DeskURL string
	Sticky  *StickyMap
	Mux     *http.ServeMux
	Client  *http.Client
}

func NewServer(engine Engine, deskURL string) *Server {
	if engine == nil {
		engine = UnavailableEngine{}
	}
	s := &Server{
		Engine:  engine,
		DeskURL: strings.TrimRight(deskURL, "/"),
		Sticky:  NewStickyMap(),
		Mux:     http.NewServeMux(),
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
	s.Mux.HandleFunc("GET /health", s.handleHealth)
	s.Mux.HandleFunc("POST /v1/worlds", s.handleLease)
	s.Mux.HandleFunc("GET /v1/worlds/{id}", s.handleGet)
	s.Mux.HandleFunc("POST /v1/worlds/{id}/heartbeat", s.handleHeartbeat)
	s.Mux.HandleFunc("POST /v1/worlds/{id}/exec", s.handleExec)
	s.Mux.HandleFunc("PUT /v1/worlds/{id}/network", s.handleNetwork)
	s.Mux.HandleFunc("DELETE /v1/worlds/{id}", s.handleDelete)
	s.Mux.HandleFunc("GET /v1/worlds/{id}/events", s.handleEvents)
	s.Mux.HandleFunc("POST /v1/worlds/{id}/snapshot", s.handleSnapshot)
	s.Mux.HandleFunc("POST /v1/worlds/{id}/restore", s.handleSnapshot)
	return s
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "router",
		"engine":  s.Engine.Available(context.Background()),
	})
}

func (s *Server) handleLease(w http.ResponseWriter, r *http.Request) {
	var lr LeaseRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&lr); err != nil && !errors.Is(err, io.EOF) {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	ApplyLeaseDefaults(&lr)
	session := r.Header.Get("X-Session-Id")
	if session == "" {
		session = lr.SessionID
	}
	if session != "" {
		if wid, ok := s.Sticky.Get(session); ok {
			info, err := s.Engine.Get(r.Context(), wid)
			if err == nil && info != nil {
				info.SessionID = session
				writeJSON(w, http.StatusOK, info)
				return
			}
			s.Sticky.UnbindSession(session)
		}
	}
	if !s.Engine.Available(r.Context()) {
		httpError(w, http.StatusServiceUnavailable, "world engine unavailable")
		return
	}
	lr.SessionID = session
	info, err := s.Engine.Lease(r.Context(), lr)
	if err != nil {
		if errors.Is(err, ErrEngineUnavailable) {
			httpError(w, http.StatusServiceUnavailable, "world engine unavailable")
			return
		}
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	if session != "" {
		s.Sticky.Bind(session, info.ID)
		info.SessionID = session
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, err := s.Engine.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrEngineUnavailable) {
			httpError(w, http.StatusServiceUnavailable, "world engine unavailable")
			return
		}
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Engine.Heartbeat(r.Context(), id); err != nil {
		if errors.Is(err, ErrEngineUnavailable) {
			httpError(w, http.StatusServiceUnavailable, "world engine unavailable")
			return
		}
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpError(w, http.StatusBadRequest, "read body")
		return
	}
	code, resp, err := s.Engine.Exec(r.Context(), id, body)
	if err != nil {
		if errors.Is(err, ErrEngineUnavailable) {
			httpError(w, http.StatusServiceUnavailable, "world engine unavailable")
			return
		}
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(resp)
}

// handleNetwork sets dark|proxy policy intent on the lease. M3 does not
// bridge guest traffic to the Compose proxy; that path is post-M3.
func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phase string `json:"phase"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch body.Phase {
	case "dark", "":
		body.Phase = "dark"
	case "proxy":
		body.Phase = "proxy"
	default:
		httpError(w, http.StatusBadRequest, "phase must be dark or proxy")
		return
	}
	info, err := s.Engine.SetNetwork(r.Context(), r.PathValue("id"), body.Phase)
	if err != nil {
		if errors.Is(err, ErrEngineUnavailable) {
			httpError(w, http.StatusServiceUnavailable, "world engine unavailable")
			return
		}
		if strings.Contains(err.Error(), "not found") {
			httpError(w, http.StatusNotFound, err.Error())
			return
		}
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := map[string]any{"id": r.PathValue("id"), "network": body.Phase}
	if info != nil {
		out["id"] = info.ID
		out["network"] = info.Network
		out["state"] = info.State
	}
	if body.Phase == "proxy" {
		proxy := strings.TrimSpace(os.Getenv("BACKLOT_EGRESS_PROXY"))
		if proxy == "" {
			proxy = "http://proxy:3128"
		}
		out["proxy"] = proxy
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Engine.Destroy(r.Context(), id); err != nil {
		if errors.Is(err, ErrEngineUnavailable) {
			httpError(w, http.StatusServiceUnavailable, "world engine unavailable")
			return
		}
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.Sticky.UnbindWorld(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "destroyed": true})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.DeskURL == "" {
		httpError(w, http.StatusServiceUnavailable, "desk unavailable")
		return
	}
	q := r.URL.Query()
	url := s.DeskURL + "/v1/events?world_id=" + id
	if c := q.Get("cursor"); c != "" {
		url += "&cursor=" + c
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := s.Client.Do(req)
	if err != nil {
		httpError(w, http.StatusBadGateway, "desk proxy failed")
		return
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(b)
}

func (s *Server) handleSnapshot(w http.ResponseWriter, _ *http.Request) {
	httpError(w, http.StatusNotImplemented, "snapshot/restore is M4")
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
