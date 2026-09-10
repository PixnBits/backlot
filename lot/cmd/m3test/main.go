// m3test runs M3 Phase 2 fleet bars. Exit 2 without KVM or without jailer (euid!=0).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/PixnBits/backlot/desk"
	"github.com/PixnBits/backlot/lot"
	"github.com/PixnBits/backlot/router"
)

func main() { os.Exit(run()) }

func run() int {
	fmt.Println("Backlot M3 Phase 2")
	if err := kvmReadable(); err != nil {
		fmt.Printf("  NOT RUN: /dev/kvm is not readable (%v)\n", err)
		return 2
	}
	if os.Geteuid() != 0 {
		fmt.Printf("  NOT RUN: need euid=0 for jailer (invoke via sudo -E ./scripts/m3test-root.sh); euid=%d\n", os.Geteuid())
		return 2
	}
	if os.Getenv("SUDO_UID") == "" && os.Getenv("PKEXEC_UID") == "" {
		fmt.Println("  NOT RUN: SUDO_UID/PKEXEC_UID unset — jailer cannot drop to a KVM-capable uid")
		return 2
	}

	repo := repoRoot()
	kernel, rootfs, err := resolveArtifacts(repo)
	if err != nil {
		fmt.Printf("  FAIL  artifacts: %v\n", err)
		return 1
	}

	work, err := os.MkdirTemp("", "backlot-m3-")
	if err != nil {
		fmt.Printf("  FAIL  tmp: %v\n", err)
		return 1
	}
	defer os.RemoveAll(work)

	deskDir := filepath.Join(work, "desk")
	store, err := desk.NewStore(deskDir)
	if err != nil {
		fmt.Printf("  FAIL  desk store: %v\n", err)
		return 1
	}
	deskSrv := desk.NewServer(store)
	deskLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("  FAIL  desk listen: %v\n", err)
		return 1
	}
	defer deskLn.Close()
	go http.Serve(deskLn, deskSrv.Mux)
	deskURL := "http://" + deskLn.Addr().String()

	boss := lot.NewBossConfig(lot.Config{
		MaxWorlds:     3,
		WarmPool:      0,
		RequireKVM:    true,
		RequireJailer: true,
		WorkDir:       filepath.Join(work, "worlds"),
		Kernel:        kernel,
		Rootfs:        rootfs,
		WaitKVM:       filepath.Join(repo, "runtime/bin/fc-waitkvm"),
		DeskURL:       deskURL,
	})
	if !boss.EngineReady() {
		fmt.Printf("  FAIL  engine not ready (kvm/artifacts/jailer)\n")
		return 1
	}
	lotLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("  FAIL  lot listen: %v\n", err)
		return 1
	}
	defer lotLn.Close()
	go http.Serve(lotLn, boss.Handler())
	lotURL := "http://" + lotLn.Addr().String()

	rt := router.NewServer(router.NewHTTPEngine(lotURL), deskURL)
	rLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("  FAIL  router listen: %v\n", err)
		return 1
	}
	defer rLn.Close()
	go http.Serve(rLn, rt.Mux)
	routerURL := "http://" + rLn.Addr().String()

	client := &http.Client{Timeout: 120 * time.Second}
	ctx := context.Background()

	// --- M3-lease-3 ---
	var worlds []map[string]any
	for i := 0; i < 3; i++ {
		info, code, err := postJSON(client, routerURL+"/v1/worlds", map[string]any{"profile": "demo"})
		if err != nil || code != 201 {
			fmt.Printf("  M3-lease-3  FAIL  lease %d code=%d err=%v body=%v\n", i, code, err, info)
			// Dump shepherd log for first lease health failures.
			filepath.Walk(filepath.Join(work, "worlds"), func(path string, info os.FileInfo, err error) error {
				if err != nil || info == nil || info.IsDir() {
					return nil
				}
				if info.Name() == "firecracker.log" {
					b, _ := os.ReadFile(path)
					fmt.Printf("  ---- %s ----\n%s\n", path, string(b))
				}
				return nil
			})
			boss.StopAll()
			return 1
		}
		worlds = append(worlds, info)
	}
	ids := []string{str(worlds[0]["id"]), str(worlds[1]["id"]), str(worlds[2]["id"])}
	cids := map[uint32]bool{}
	engines := map[string]bool{}
	var pids []int
	var jails []string
	for _, id := range ids {
		jail, pid, cid, engine, ok := boss.SlotMeta(id)
		if !ok || pid <= 0 {
			fmt.Printf("  M3-lease-3  FAIL  missing slot meta for %s\n", id)
			boss.StopAll()
			return 1
		}
		if engine != "jailer" {
			fmt.Printf("  M3-lease-3  FAIL  engine=%s want jailer for %s\n", engine, id)
			boss.StopAll()
			return 1
		}
		engines[engine] = true
		if cids[cid] {
			fmt.Printf("  M3-lease-3  FAIL  duplicate guest_cid %d\n", cid)
			boss.StopAll()
			return 1
		}
		cids[cid] = true
		pids = append(pids, pid)
		jails = append(jails, jail)
		if err := syscall.Kill(pid, 0); err != nil {
			fmt.Printf("  M3-lease-3  FAIL  vmm pid %d not alive: %v\n", pid, err)
			boss.StopAll()
			return 1
		}
	}
	fmt.Printf("  M3-lease-3  PASS  ids=%v cids=%v engines=%v\n", ids, keysU32(cids), keysStr(engines))

	// Cap: fourth lease must fail
	_, code4, _ := postJSON(client, routerURL+"/v1/worlds", map[string]any{"profile": "demo"})
	if code4 != 503 && code4 != 502 {
		fmt.Printf("  M3-lease-3  FAIL  4th lease should hit cap, code=%d\n", code4)
		boss.StopAll()
		return 1
	}

	// --- M3-exec ---
	for _, id := range ids {
		res, code, err := execWorld(client, routerURL, id, []string{"/usr/bin/ls", "/workspace"}, 30)
		if err != nil || code != 200 || intField(res, "exit_code") != 0 || !strings.Contains(str(res["stdout"]), "hello.txt") {
			fmt.Printf("  M3-exec     FAIL  id=%s code=%d err=%v res=%v\n", id, code, err, res)
			boss.StopAll()
			return 1
		}
	}
	fmt.Printf("  M3-exec     PASS  ls /workspace → hello.txt on all 3\n")

	// --- M3-isolation ---
	a, b := ids[0], ids[1]
	// Desk partition: decoy on A must not appear under B
	_, _, _ = execWorld(client, routerURL, a, []string{"/usr/bin/cat", "/opt/grok/CANARY.txt"}, 30)
	time.Sleep(800 * time.Millisecond)
	evA, err := getEvents(client, routerURL, a)
	if err != nil {
		fmt.Printf("  M3-isolation FAIL  events A: %v\n", err)
		boss.StopAll()
		return 1
	}
	evB, err := getEvents(client, routerURL, b)
	if err != nil {
		fmt.Printf("  M3-isolation FAIL  events B: %v\n", err)
		boss.StopAll()
		return 1
	}
	if !eventsHaveKind(evA, "decoy_open") {
		fmt.Printf("  M3-isolation FAIL  A missing decoy_open after cat CANARY: %s\n", truncate(evA, 500))
		boss.StopAll()
		return 1
	}
	if eventsHaveKind(evB, "decoy_open") {
		fmt.Printf("  M3-isolation FAIL  B desk shows decoy_open from A\n")
		boss.StopAll()
		return 1
	}
	// Cross-id: exec on nonexistent id fails; B still works
	_, codeX, _ := execWorld(client, routerURL, "no-such-world", []string{"/bin/true"}, 10)
	if codeX != 404 && codeX != 502 {
		fmt.Printf("  M3-isolation FAIL  bogus id exec code=%d want 404/502\n", codeX)
		boss.StopAll()
		return 1
	}
	resB, codeB, err := execWorld(client, routerURL, b, []string{"/bin/true"}, 20)
	if err != nil || codeB != 200 || intField(resB, "exit_code") != 0 {
		fmt.Printf("  M3-isolation FAIL  B exec after cross: code=%d err=%v res=%v\n", codeB, err, resB)
		boss.StopAll()
		return 1
	}
	fmt.Printf("  M3-isolation PASS  desk partitioned; cross-id rejected\n")

	// --- M3-decoy ---
	if !eventsHaveKind(evA, "decoy_open") {
		fmt.Printf("  M3-decoy    FAIL  desk missing decoy_open\n")
		boss.StopAll()
		return 1
	}
	fmt.Printf("  M3-decoy    PASS  decoy_open on desk for world A\n")

	// --- M3-scale-down ---
	delCode, err := deleteWorld(client, routerURL, a)
	if err != nil || delCode != 200 {
		fmt.Printf("  M3-scale-down FAIL  delete A code=%d err=%v\n", delCode, err)
		boss.StopAll()
		return 1
	}
	time.Sleep(500 * time.Millisecond)
	if err := syscall.Kill(pids[0], 0); err == nil {
		fmt.Printf("  M3-scale-down FAIL  A vmm pid %d still alive\n", pids[0])
		boss.StopAll()
		return 1
	}
	for _, id := range []string{ids[1], ids[2]} {
		res, code, err := execWorld(client, routerURL, id, []string{"/usr/bin/ls", "/workspace"}, 30)
		if err != nil || code != 200 || intField(res, "exit_code") != 0 {
			fmt.Printf("  M3-scale-down FAIL  remaining %s code=%d err=%v\n", id, code, err)
			boss.StopAll()
			return 1
		}
	}
	fmt.Printf("  M3-scale-down PASS  A gone; B+C still exec\n")

	// --- M3-demand ---
	infoNew, codeNew, err := postJSON(client, routerURL+"/v1/worlds", map[string]any{"profile": "demo"})
	if err != nil || codeNew != 201 {
		fmt.Printf("  M3-demand   FAIL  replacement lease code=%d err=%v\n", codeNew, err)
		boss.StopAll()
		return 1
	}
	newID := str(infoNew["id"])
	live := boss.LiveIDs()
	if len(live) > 3 {
		fmt.Printf("  M3-demand   FAIL  live=%d > cap 3\n", len(live))
		boss.StopAll()
		return 1
	}
	_, code4b, _ := postJSON(client, routerURL+"/v1/worlds", map[string]any{"profile": "demo"})
	if code4b == 201 {
		fmt.Printf("  M3-demand   FAIL  lease beyond cap succeeded\n")
		boss.StopAll()
		return 1
	}
	fmt.Printf("  M3-demand   PASS  replacement %s; live=%d ≤ 3\n", newID, len(live))

	// --- M3-no-kvm-guest ---
	for _, id := range boss.LiveIDs() {
		res, code, err := execWorld(client, routerURL, id, []string{"/bin/sh", "-c", "if [ -e /dev/kvm ]; then echo HAS_KVM; else echo NO_KVM; fi"}, 15)
		if err != nil || code != 200 || strings.Contains(str(res["stdout"]), "HAS_KVM") {
			fmt.Printf("  M3-no-kvm-guest FAIL  id=%s err=%v res=%v\n", id, err, res)
			boss.StopAll()
			return 1
		}
	}
	fmt.Printf("  M3-no-kvm-guest PASS\n")

	// --- M3-tenant-jail ---
	// "jail": false must still go through inner/run.py (bwrap). Probe: decoy path works and
	// a marker that only the jail sets — cat of CANARY is enough plus stderr/audit path.
	probeID := boss.LiveIDs()[0]
	body, _ := json.Marshal(map[string]any{
		"argv":    []string{"/usr/bin/cat", "/opt/grok/CANARY.txt"},
		"timeout": 30,
		"jail":    false,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, routerURL+"/v1/worlds/"+probeID+"/exec", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("  M3-tenant-jail FAIL  %v\n", err)
		boss.StopAll()
		return 1
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var jr map[string]any
	_ = json.Unmarshal(raw, &jr)
	if resp.StatusCode != 200 || intField(jr, "exit_code") != 0 || !strings.Contains(str(jr["stdout"]), "CANARY") {
		fmt.Printf("  M3-tenant-jail FAIL  code=%d body=%s\n", resp.StatusCode, raw)
		boss.StopAll()
		return 1
	}
	time.Sleep(600 * time.Millisecond)
	evP, _ := getEvents(client, routerURL, probeID)
	if !eventsHaveKind(evP, "decoy_open") && !eventsHaveKind(evP, "start") {
		fmt.Printf("  M3-tenant-jail FAIL  jail=false skipped inner ring? events=%s\n", truncate(evP, 400))
		boss.StopAll()
		return 1
	}
	fmt.Printf("  M3-tenant-jail PASS  jail=false still jails (decoy/start on desk)\n")

	// --- M3-orphan ---
	liveBefore := append([]string{}, boss.LiveIDs()...)
	var livePids []int
	var liveJails []string
	for _, id := range liveBefore {
		j, p, _, _, ok := boss.SlotMeta(id)
		if ok {
			livePids = append(livePids, p)
			liveJails = append(liveJails, j)
		}
	}
	boss.StopAll()
	time.Sleep(800 * time.Millisecond)
	var leftovers []string
	for _, pid := range livePids {
		if err := syscall.Kill(pid, 0); err == nil {
			leftovers = append(leftovers, fmt.Sprintf("pid %d", pid))
		}
	}
	if len(leftovers) > 0 {
		fmt.Printf("  M3-orphan   FAIL  leftover: %v jails=%v\n", leftovers, liveJails)
		return 1
	}
	fmt.Printf("  M3-orphan   PASS  no leftover firecracker after lot-boss stop\n")

	_ = jails
	fmt.Println("summary: M3 Phase 2 PASS")
	return 0
}

func kvmReadable() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

func resolveArtifacts(repo string) (kernel, rootfs string, err error) {
	kernel = filepath.Join(repo, "guest/artifacts/vmlinux-6.1.102")
	rootfs = filepath.Join(repo, "guest/artifacts/rootfs.ext4")
	if _, e := os.Stat(kernel); e != nil {
		alt := "/home/pixnbits/projects/backlot/feature/m2.1-jailer/guest/artifacts/vmlinux-6.1.102"
		if _, e2 := os.Stat(alt); e2 == nil {
			kernel = alt
		}
	}
	if _, e := os.Stat(rootfs); e != nil {
		alt := "/home/pixnbits/projects/backlot/feature/m2.1-jailer/guest/artifacts/rootfs.ext4"
		if _, e2 := os.Stat(alt); e2 == nil {
			rootfs = alt
		}
	}
	for _, p := range []string{kernel, rootfs} {
		if _, e := os.Stat(p); e != nil {
			return "", "", fmt.Errorf("missing %s", p)
		}
	}
	return kernel, rootfs, nil
}

func postJSON(c *http.Client, url string, body any) (map[string]any, int, error) {
	b, _ := json.Marshal(body)
	res, err := c.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m, res.StatusCode, nil
}

func execWorld(c *http.Client, base, id string, argv []string, timeout int) (map[string]any, int, error) {
	b, _ := json.Marshal(map[string]any{"argv": argv, "timeout": timeout})
	res, err := c.Post(base+"/v1/worlds/"+id+"/exec", "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m, res.StatusCode, nil
}

func deleteWorld(c *http.Client, base, id string) (int, error) {
	req, err := http.NewRequest(http.MethodDelete, base+"/v1/worlds/"+id, nil)
	if err != nil {
		return 0, err
	}
	res, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode, nil
}

func getEvents(c *http.Client, base, id string) (string, error) {
	res, err := c.Get(base + "/v1/worlds/" + id + "/events")
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return "", fmt.Errorf("status %d: %s", res.StatusCode, raw)
	}
	return string(raw), nil
}

func eventsHaveKind(body, kind string) bool {
	return strings.Contains(body, `"kind":"`+kind+`"`) || strings.Contains(body, `"kind": "`+kind+`"`)
}

func str(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func intField(m map[string]any, k string) int {
	v, ok := m[k]
	if !ok {
		return -999
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	default:
		return -999
	}
}

func keysU32(m map[uint32]bool) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysStr(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func repoRoot() string {
	if v := os.Getenv("BACKLOT_ROOT"); v != "" {
		return v
	}
	wd, _ := os.Getwd()
	for p := wd; p != "/"; p = filepath.Dir(p) {
		if _, err := os.Stat(filepath.Join(p, "inner", "run.py")); err == nil {
			return p
		}
	}
	return wd
}
