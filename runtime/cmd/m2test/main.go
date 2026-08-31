// m2test runs M2-boot through M2-orphan. Exit 2 if no /dev/kvm. Exit 1 on fail.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/PixnBits/backlot/runtime/world"
)

func main() {
	os.Exit(run())
}

func run() int {
	fmt.Println("Backlot M2")
	if err := kvmReadable(); err != nil {
		fmt.Printf("  NOT RUN: /dev/kvm is not readable (%v)\n", err)
		return 2
	}

	repo := repoRoot()
	kernel := filepath.Join(repo, "guest/artifacts/vmlinux-6.1.102")
	rootfs := filepath.Join(repo, "guest/artifacts/rootfs.ext4")
	for _, p := range []string{kernel, rootfs} {
		if _, err := os.Stat(p); err != nil {
			fmt.Printf("  FAIL  missing %s\n", p)
			return 1
		}
	}

	work, err := os.MkdirTemp("", "backlot-m2-")
	if err != nil {
		fmt.Printf("  FAIL  tmp: %v\n", err)
		return 1
	}
	defer os.RemoveAll(work)

	fc := getenv("FIRECRACKER_BIN", "/usr/local/firecracker/v1.15.1/firecracker")
	jailer := getenv("JAILER_BIN", "/usr/local/firecracker/v1.15.1/jailer")

	w, err := world.Start(world.StartOpts{
		ID:          "demo",
		WorkDir:     work,
		Kernel:      kernel,
		Rootfs:      rootfs,
		Firecracker: fc,
		Jailer:      jailer,
	})
	if err != nil {
		fmt.Printf("  M2-boot     FAIL  %v\n", err)
		dumpLog(work)
		return 1
	}
	fmt.Printf("  M2-boot     PASS  engine=%s\n", w.Engine)

	ctx := context.Background()
	res, err := w.Exec(ctx, []string{"/usr/bin/ls", "/workspace"}, 20)
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, "hello.txt") {
		fmt.Printf("  M2-exec     FAIL  err=%v res=%+v\n", err, res)
		w.Stop()
		dumpLog(work)
		return 1
	}
	fmt.Printf("  M2-exec     PASS  ls /workspace → hello.txt\n")

	// T1–T10 inside the guest via inner/run.py (run_int.py compiles in-guest).
	inner, err := w.ExecOpt(ctx, []string{"python3", "/opt/backlot/inner/tests/int/run_int.py"}, 120, false)
	if err != nil {
		fmt.Printf("  M2-inner    FAIL  %v\n", err)
		w.Stop()
		dumpLog(work)
		return 1
	}
	innerOut := inner.Stdout + inner.Stderr
	if inner.ExitCode != 0 || !strings.Contains(innerOut, "0 fail") {
		fmt.Printf("  M2-inner    FAIL  rc=%d\n%s\n", inner.ExitCode, innerOut)
		w.Stop()
		dumpLog(work)
		return 1
	}
	fmt.Printf("  M2-inner    PASS  %s\n", lastLine(innerOut))

	_, _ = w.Exec(ctx, []string{"/usr/bin/cat", "/opt/grok/CANARY.txt"}, 20)
	time.Sleep(400 * time.Millisecond)
	hostLog, _ := os.ReadFile(w.EventsPath)
	if !strings.Contains(string(hostLog), "decoy_open") && !strings.Contains(string(hostLog), `"kind":"decoy_open"`) {
		// inner forwards the whole event as payload; kind may be nested.
		if !strings.Contains(string(hostLog), "decoy_open") {
			fmt.Printf("  M2-decoy-host FAIL  host jsonl missing decoy_open\n%s\n", hostLog)
			w.Stop()
			return 1
		}
	}
	fmt.Printf("  M2-decoy-host PASS  decoy_open in host jsonl\n")

	if !strings.Contains(string(hostLog), `"kind":"start"`) && !strings.Contains(string(hostLog), `"kind":"exit"`) {
		if !(strings.Contains(string(hostLog), `"start"`) && strings.Contains(string(hostLog), `"exit"`)) {
			fmt.Printf("  M2-start-exit FAIL  host jsonl:\n%s\n", hostLog)
			w.Stop()
			return 1
		}
	}
	fmt.Printf("  M2-start-exit PASS\n")

	kvm, err := w.Exec(ctx, []string{"/bin/sh", "-c", "if [ -e /dev/kvm ]; then echo HAS_KVM; else echo NO_KVM; fi"}, 10)
	if err != nil || strings.Contains(kvm.Stdout, "HAS_KVM") {
		fmt.Printf("  M2-no-kvm   FAIL  err=%v out=%q\n", err, kvm)
		w.Stop()
		return 1
	}
	fmt.Printf("  M2-no-kvm   PASS  guest has no /dev/kvm\n")

	// Host jsonl path must not exist in the guest.
	probe := fmt.Sprintf("if [ -e %q ]; then echo HAS_HOST_JSONL; else echo NO_HOST_JSONL; fi", w.EventsPath)
	aud, err := w.Exec(ctx, []string{"/bin/sh", "-c", probe}, 10)
	if err != nil || strings.Contains(aud.Stdout, "HAS_HOST_JSONL") {
		fmt.Printf("  M2-no-audit FAIL  err=%v out=%q\n", err, aud)
		w.Stop()
		return 1
	}
	fmt.Printf("  M2-no-audit PASS  host jsonl path absent from guest\n")

	fcPid := w.CmdPid()
	jail := w.JailRoot
	w.Stop()
	time.Sleep(500 * time.Millisecond)
	if orphans := leftoverOur(fcPid, jail); len(orphans) > 0 {
		fmt.Printf("  M2-orphan   FAIL  leftover: %v\n", orphans)
		return 1
	}
	fmt.Printf("  M2-orphan   PASS  no leftover firecracker after SIGTERM\n")
	fmt.Println("summary: M2 PASS")
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"events": w.EventsPath, "engine": w.Engine})
	return 0
}

func kvmReadable() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

func leftoverOur(pid int, jailRoot string) []string {
	var leftover []string
	if pid > 0 {
		if err := syscall.Kill(pid, 0); err == nil {
			leftover = append(leftover, fmt.Sprintf("vmm pid %d still alive", pid))
		}
	}
	out, _ := exec.Command("pgrep", "-a", "firecracker").Output()
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if strings.Contains(ln, jailRoot) {
			leftover = append(leftover, ln)
		}
	}
	return leftover
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

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

func dumpLog(work string) {
	b, err := os.ReadFile(filepath.Join(work, "firecracker.log"))
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "--- firecracker.log ---\n%s\n", b)
}
