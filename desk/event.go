package desk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// GenesisPrev is the prev_hash for the first event in a world's chain.
const GenesisPrev = "0000000000000000000000000000000000000000000000000000000000000000"

// Event is one committed continuity record. Fields match PRD §6.6 / §15.4.
type Event struct {
	UTC         string          `json:"utc"`
	Seq         uint64          `json:"seq"`
	WorldID     string          `json:"world_id"`
	SessionID   string          `json:"session_id,omitempty"`
	PolicyHash  string          `json:"policy_hash,omitempty"`
	Kind        string          `json:"kind"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	PrevHash    string          `json:"prev_hash"`
	EventHash   string          `json:"event_hash"`
}

// IngestRequest is what an emitter sends; desk fills seq, hashes, utc if empty.
type IngestRequest struct {
	WorldID    string          `json:"world_id"`
	SessionID  string          `json:"session_id,omitempty"`
	PolicyHash string          `json:"policy_hash,omitempty"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	UTC        string          `json:"utc,omitempty"`
}

type chainMaterial struct {
	UTC        string          `json:"utc"`
	Seq        uint64          `json:"seq"`
	WorldID    string          `json:"world_id"`
	SessionID  string          `json:"session_id,omitempty"`
	PolicyHash string          `json:"policy_hash,omitempty"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	PrevHash   string          `json:"prev_hash"`
}

func hashEvent(utc string, seq uint64, worldID, sessionID, policyHash, kind string, payload json.RawMessage, prevHash string) (string, error) {
	if payload == nil {
		payload = json.RawMessage("null")
	}
	mat := chainMaterial{
		UTC:        utc,
		Seq:        seq,
		WorldID:    worldID,
		SessionID:  sessionID,
		PolicyHash: policyHash,
		Kind:       kind,
		Payload:    payload,
		PrevHash:   prevHash,
	}
	b, err := json.Marshal(mat)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func nowUTC() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000000Z")
}

// VerifyChain walks events in order and returns an error if any hash is wrong.
func VerifyChain(events []Event) error {
	prev := GenesisPrev
	for i, ev := range events {
		want, err := hashEvent(ev.UTC, ev.Seq, ev.WorldID, ev.SessionID, ev.PolicyHash, ev.Kind, ev.Payload, ev.PrevHash)
		if err != nil {
			return err
		}
		if ev.PrevHash != prev {
			return fmt.Errorf("event %d: prev_hash mismatch: got %s want %s", i, ev.PrevHash, prev)
		}
		if ev.EventHash != want {
			return fmt.Errorf("event %d: event_hash mismatch: got %s want %s", i, ev.EventHash, want)
		}
		prev = ev.EventHash
	}
	return nil
}
