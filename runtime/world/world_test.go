package world

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFcConfigDefaultHasNoBareExec(t *testing.T) {
	b, err := fcConfigJSON("")
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "backlot.bare_exec") {
		t.Fatalf("default shepherd config must not set backlot.bare_exec:\n%s", got)
	}
	if !strings.Contains(got, defaultBootArgs) {
		t.Fatalf("missing default boot_args:\n%s", got)
	}
}

func TestFcConfigAppendsExtraBootArgs(t *testing.T) {
	b, err := fcConfigJSON("backlot.bare_exec=1")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	bs := cfg["boot-source"].(map[string]any)
	args := bs["boot_args"].(string)
	want := defaultBootArgs + " backlot.bare_exec=1"
	if args != want {
		t.Fatalf("boot_args=%q want %q", args, want)
	}
}

func TestExecBodyHasNoJailKey(t *testing.T) {
	body := execBody([]string{"/bin/true"}, 5)
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["jail"]; ok {
		t.Fatalf("wire JSON must not include jail: %s", body)
	}
	if _, ok := m["argv"]; !ok {
		t.Fatalf("missing argv: %s", body)
	}
	if _, ok := m["timeout"]; !ok {
		t.Fatalf("missing timeout: %s", body)
	}
}
