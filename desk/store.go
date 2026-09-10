package desk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Store is an append-only event sink. Events are never UPDATE/DELETE'd.
type Store struct {
	mu   sync.Mutex
	root string
	seq  uint64
	tip  map[string]string // world_id → last event_hash
}

func NewStore(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "worlds"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "meta"), 0o700); err != nil {
		return nil, err
	}
	s := &Store{root: root, tip: map[string]string{}}
	if err := s.replay(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) globalPath() string {
	return filepath.Join(s.root, "global.jsonl")
}

func (s *Store) worldPath(worldID string) string {
	// Sanitize path segment: only allow safe id chars.
	safe := sanitizeID(worldID)
	return filepath.Join(s.root, "worlds", safe+".jsonl")
}

func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "_empty"
	}
	return out
}

func (s *Store) replay() error {
	f, err := os.Open(s.globalPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// Allow long event lines.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return fmt.Errorf("replay: %w", err)
		}
		if ev.Seq > s.seq {
			s.seq = ev.Seq
		}
		s.tip[ev.WorldID] = ev.EventHash
	}
	return sc.Err()
}

// Commit appends one event. No overwrite path exists.
func (s *Store) Commit(req IngestRequest) (*Event, error) {
	if req.WorldID == "" {
		return nil, fmt.Errorf("world_id required")
	}
	if req.Kind == "" {
		return nil, fmt.Errorf("kind required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	utc := req.UTC
	if utc == "" {
		utc = nowUTC()
	}
	prev := s.tip[req.WorldID]
	if prev == "" {
		prev = GenesisPrev
	}
	s.seq++
	seq := s.seq
	payload := req.Payload
	if payload == nil {
		payload = json.RawMessage("null")
	}
	h, err := hashEvent(utc, seq, req.WorldID, req.SessionID, req.PolicyHash, req.Kind, payload, prev)
	if err != nil {
		s.seq--
		return nil, err
	}
	ev := &Event{
		UTC:        utc,
		Seq:        seq,
		WorldID:    req.WorldID,
		SessionID:  req.SessionID,
		PolicyHash: req.PolicyHash,
		Kind:       req.Kind,
		Payload:    payload,
		PrevHash:   prev,
		EventHash:  h,
	}
	line, err := json.Marshal(ev)
	if err != nil {
		s.seq--
		return nil, err
	}
	line = append(line, '\n')

	if err := appendFile(s.globalPath(), line); err != nil {
		s.seq--
		return nil, err
	}
	if err := appendFile(s.worldPath(req.WorldID), line); err != nil {
		// Global already committed; keep tip consistent with global.
		// World file failure is loud — operator must repair from global.
		return nil, fmt.Errorf("world append after global commit: %w", err)
	}
	s.tip[req.WorldID] = h
	return ev, nil
}

func appendFile(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := f.Write(line)
	if err != nil {
		return err
	}
	if n != len(line) {
		return fmt.Errorf("short write")
	}
	return f.Sync()
}

// ListWorld returns events for one world with seq > cursor.
// A caller cannot read another world: only this world's file is opened.
func (s *Store) ListWorld(worldID string, cursor uint64) ([]Event, error) {
	if worldID == "" {
		return nil, fmt.Errorf("world_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.worldPath(worldID))
	if err != nil {
		if os.IsNotExist(err) {
			return []Event{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Event
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, err
		}
		if ev.WorldID != worldID {
			return nil, fmt.Errorf("corrupt world file: foreign world_id")
		}
		if ev.Seq > cursor {
			out = append(out, ev)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// LoadWorldChain loads all events for a world (for tamper checks).
func (s *Store) LoadWorldChain(worldID string) ([]Event, error) {
	return s.ListWorld(worldID, 0)
}
