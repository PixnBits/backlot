package desk

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashChainCommitAndVerify(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		payload, _ := json.Marshal(map[string]int{"n": i})
		if _, err := store.Commit(IngestRequest{WorldID: "w1", Kind: "test", Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.LoadWorldChain("w1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events", len(events))
	}
	if err := VerifyChain(events); err != nil {
		t.Fatal(err)
	}
}

func TestHashChainTamperDetected(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(IngestRequest{WorldID: "w1", Kind: "start", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(IngestRequest{WorldID: "w1", Kind: "decoy_open", Payload: json.RawMessage(`{"path":"/etc/shadow"}`)}); err != nil {
		t.Fatal(err)
	}

	worldFile := filepath.Join(dir, "worlds", "w1.jsonl")
	f, err := os.Open(worldFile)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	_ = f.Close()
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}

	// Tamper: rewrite the second event's payload while leaving event_hash intact.
	var ev Event
	if err := json.Unmarshal([]byte(lines[1]), &ev); err != nil {
		t.Fatal(err)
	}
	ev.Payload = json.RawMessage(`{"path":"/etc/TAMPERED"}`)
	// Keep the old EventHash so VerifyChain must notice material drift.
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	tampered := lines[0] + "\n" + string(b) + "\n"
	if err := os.WriteFile(worldFile, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}

	events, err := store.LoadWorldChain("w1")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyChain(events); err == nil {
		t.Fatal("expected tamper detection, got nil")
	} else if !strings.Contains(err.Error(), "event_hash mismatch") && !strings.Contains(err.Error(), "prev_hash mismatch") {
		t.Fatalf("unexpected verify error: %v", err)
	}
}

func TestWorldIsolation(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(IngestRequest{WorldID: "alice", Kind: "start", Payload: json.RawMessage(`{"who":"a"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(IngestRequest{WorldID: "bob", Kind: "start", Payload: json.RawMessage(`{"who":"b"}`)}); err != nil {
		t.Fatal(err)
	}
	alice, err := store.ListWorld("alice", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(alice) != 1 || alice[0].WorldID != "alice" {
		t.Fatalf("alice events: %+v", alice)
	}
	bob, err := store.ListWorld("bob", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(bob) != 1 || bob[0].WorldID != "bob" {
		t.Fatalf("bob events: %+v", bob)
	}
	if alice[0].Seq == bob[0].Seq {
		t.Fatal("global seq must differ across commits")
	}
}

func TestNoDeleteAPI(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := store.Commit(IngestRequest{WorldID: "w", Kind: "x", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Seq != 1 {
		t.Fatalf("seq=%d", ev.Seq)
	}
}
