package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestExecReqIgnoresJailKeyAndHasNoJailField(t *testing.T) {
	var req execReq
	if err := json.Unmarshal([]byte(`{"argv":["/bin/true"],"jail":false}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Argv) != 1 || req.Argv[0] != "/bin/true" {
		t.Fatalf("argv=%v", req.Argv)
	}
	st := reflect.TypeOf(req)
	for i := 0; i < st.NumField(); i++ {
		if st.Field(i).Name == "Jail" {
			t.Fatal("execReq must not have a Jail field")
		}
	}
	name, args := tenantExecArgs("demo", "/run/backlot/inner-demo.jsonl", req.Argv)
	if name != "python3" || len(args) < 1 || args[0] != innerRun {
		t.Fatalf("jail:false still must jail via %s: name=%s args=%v", innerRun, name, args)
	}
}

func TestBareExecUnregisteredWithoutEnv(t *testing.T) {
	t.Setenv("BACKLOT_BARE_EXEC", "")
	mux := newMux(&emitter{})
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/bare-exec", strings.NewReader(`{"argv":["/bin/true"]}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404 when BACKLOT_BARE_EXEC is unset", rr.Code)
	}
}

func TestBareExecRegisteredWithEnv(t *testing.T) {
	t.Setenv("BACKLOT_BARE_EXEC", "1")
	mux := newMux(&emitter{})
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/bare-exec", strings.NewReader(`{`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400 from registered bare-exec handler", rr.Code)
	}
}
