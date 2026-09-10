package desk

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// Server is the continuity desk HTTP API.
type Server struct {
	Store *Store
	Mux   *http.ServeMux
}

func NewServer(store *Store) *Server {
	s := &Server{Store: store, Mux: http.NewServeMux()}
	s.Mux.HandleFunc("GET /health", s.handleHealth)
	s.Mux.HandleFunc("POST /v1/events", s.handleIngest)
	s.Mux.HandleFunc("GET /v1/events", s.handleList)
	return s
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"service":"desk"}`))
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	var req IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	ev, err := s.Store.Commit(req)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ev)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	worldID := r.URL.Query().Get("world_id")
	if worldID == "" {
		httpError(w, http.StatusBadRequest, "world_id required")
		return
	}
	var cursor uint64
	if c := r.URL.Query().Get("cursor"); c != "" {
		n, err := strconv.ParseUint(c, 10, 64)
		if err != nil {
			httpError(w, http.StatusBadRequest, "bad cursor")
			return
		}
		cursor = n
	}
	events, err := s.Store.ListWorld(worldID, cursor)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"world_id": worldID, "events": events})
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
