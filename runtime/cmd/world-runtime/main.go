// Guest world-runtime. Listens on AF_VSOCK:8080.
// Emits start/decoy_open/exit to the host over AF_VSOCK to CID 2:5252.
// Does not write the host jsonl. Does not see /dev/kvm.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/mdlayher/vsock"
)

const (
	hostCID    = 2
	eventPort  = 5252
	listenPort = 8080
	innerRun   = "/opt/backlot/inner/run.py"
	workspace  = "/workspace"
)

func main() {
	log.SetPrefix("world-runtime ")
	log.SetFlags(0)

	emit := connectEmitter()
	defer emit.Close()

	ln, err := vsock.Listen(listenPort, nil)
	if err != nil {
		log.Fatalf("vsock listen %d: %v", listenPort, err)
	}
	log.Printf("listening vsock :%d", listenPort)

	mux := newMux(emit)

	s := &http.Server{Handler: mux, ReadHeaderTimeout: 15 * time.Second}
	if err := s.Serve(ln); err != nil {
		log.Fatal(err)
	}
}

func notThisSlice(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte(`{"error":"not M2; 501"}`))
}

func newMux(emit *emitter) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /v1/worlds/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		handleExec(w, r, emit)
	})
	// Internal harness path. Registered only when the guest was booted with
	// backlot.bare_exec=1 (init.sh exports BACKLOT_BARE_EXEC=1). Else 404.
	if os.Getenv("BACKLOT_BARE_EXEC") == "1" {
		mux.HandleFunc("POST /v1/internal/bare-exec", func(w http.ResponseWriter, r *http.Request) {
			handleBareExec(w, r, emit)
		})
	}
	mux.HandleFunc("POST /v1/worlds/{id}/heartbeat", notThisSlice)
	mux.HandleFunc("POST /v1/worlds/{id}/files", notThisSlice)
	mux.HandleFunc("GET /v1/worlds/{id}/files", notThisSlice)
	mux.HandleFunc("PUT /v1/worlds/{id}/network", notThisSlice)
	mux.HandleFunc("DELETE /v1/worlds/{id}", notThisSlice)
	mux.HandleFunc("GET /v1/worlds/{id}/events", notThisSlice)
	mux.HandleFunc("POST /v1/worlds", notThisSlice)
	mux.HandleFunc("GET /v1/worlds/{id}", notThisSlice)
	return mux
}

type execReq struct {
	Argv    []string `json:"argv"`
	Stdin   string   `json:"stdin,omitempty"`
	Timeout int      `json:"timeout,omitempty"`
	// No Jail field. Leftover "jail": false in JSON is ignored (unknown field)
	// and the tenant path still jails via inner/run.py.
}

type execResp struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type emitter struct {
	mu   sync.Mutex
	conn io.WriteCloser
}

func (e *emitter) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conn != nil {
		_ = e.conn.Close()
	}
}

func (e *emitter) send(kind string, payload any) {
	line := map[string]any{
		"utc":     time.Now().UTC().Format("2006-01-02T15:04:05.000000Z"),
		"kind":    kind,
		"payload": payload,
	}
	b, err := json.Marshal(line)
	if err != nil {
		log.Printf("emit marshal: %v", err)
		return
	}
	b = append(b, '\n')
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conn == nil {
		return
	}
	_, err = e.conn.Write(b)
	if err != nil {
		log.Printf("emit write: %v", err)
	}
}

func connectEmitter() *emitter {
	e := &emitter{}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		c, err := vsock.Dial(hostCID, eventPort, nil)
		if err == nil {
			e.conn = c
			log.Printf("event vsock connected host:%d", eventPort)
			e.send("runtime_up", map[string]string{"role": "guest"})
			return e
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.Printf("event vsock not connected (host not listening yet); exec still works, host jsonl will miss events")
	return e
}

func parseExecReq(w http.ResponseWriter, r *http.Request) (execReq, context.Context, context.CancelFunc, bool) {
	var req execReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return req, nil, nil, false
	}
	if len(req.Argv) == 0 {
		http.Error(w, "argv required", 400)
		return req, nil, nil, false
	}
	timeout := time.Duration(req.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	return req, ctx, cancel, true
}

// tenantExecArgs always wraps argv in inner/run.py. Leftover "jail": false
// in the JSON cannot opt out — execReq has no Jail field.
func tenantExecArgs(worldID, audit string, argv []string) (name string, args []string) {
	args = []string{innerRun,
		"--workspace", workspace,
		"--audit", audit,
		"--sandbox-id", worldID,
		"--",
	}
	args = append(args, argv...)
	return "python3", args
}

func handleExec(w http.ResponseWriter, r *http.Request, emit *emitter) {
	worldID := r.PathValue("id")
	req, ctx, cancel, ok := parseExecReq(w, r)
	if !ok {
		return
	}
	defer cancel()

	audit := filepath.Join("/run/backlot", "inner-"+worldID+".jsonl")
	_ = os.Remove(audit)

	name, args := tenantExecArgs(worldID, audit, req.Argv)
	cmd := exec.CommandContext(ctx, name, args...)
	respondExec(w, cmd, req, emit, audit)
}

func handleBareExec(w http.ResponseWriter, r *http.Request, emit *emitter) {
	req, ctx, cancel, ok := parseExecReq(w, r)
	if !ok {
		return
	}
	defer cancel()
	cmd := exec.CommandContext(ctx, req.Argv[0], req.Argv[1:]...)
	respondExec(w, cmd, req, emit, "")
}

func respondExec(w http.ResponseWriter, cmd *exec.Cmd, req execReq, emit *emitter, audit string) {
	cmd.Dir = workspace
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if req.Stdin != "" {
		cmd.Stdin = bytes.NewBufferString(req.Stdin)
	}

	err := cmd.Run()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
			fmt.Fprintf(&stderr, "\nrun: %v\n", err)
		}
	}

	// Forward inner/run.py audit lines to the host. Guest file stays in /run/backlot.
	if audit != "" {
		forwardAudit(audit, emit)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(execResp{
		ExitCode: exit,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	})
}

func forwardAudit(path string, emit *emitter) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		kind, _ := ev["kind"].(string)
		if kind == "" {
			continue
		}
		emit.send(kind, ev)
	}
}
