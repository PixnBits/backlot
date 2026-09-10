package world

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/PixnBits/backlot/runtime/vsockhost"
)

const (
	ExecPort  = 8080
	EventPort = 5252
)

type StartOpts struct {
	ID, WorkDir, Kernel, Rootfs, Firecracker, Jailer string
	// WaitKVM is the jailer --exec-file helper; blocks until overlay bind live, then execs /firecracker.real.
	WaitKVM string
	// GuestCID is the Firecracker vsock guest CID (must be >= 3 and unique on the host).
	// Zero means default 3 (single-world / M2).
	GuestCID uint32
	// BareExec appends backlot.bare_exec=1 to the Firecracker kernel cmdline.
	// Product shepherd leaves this false. Only m2test sets it so the guest
	// can expose /v1/internal/bare-exec.
	BareExec bool
}

type World struct {
	ID         string
	EventsPath string
	JailRoot   string
	UDS        string
	GuestCID   uint32
	Engine     string // "jailer" or "firecracker"
	cmd        *exec.Cmd
	eventLn    net.Listener
	client     *http.Client
	kvmOverlay string
}

func Start(opts StartOpts) (*World, error) {
	if err := os.MkdirAll(opts.WorkDir, 0o700); err != nil {
		return nil, err
	}
	events := filepath.Join(opts.WorkDir, "events.jsonl")
	ef, err := os.OpenFile(events, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	_ = ef.Close()

	chrootBase := filepath.Join(opts.WorkDir, "jails")
	// Jailer places the chroot at <base>/<basename(exec-file)>/<id>/root.
	jailFolder := "firecracker"
	if os.Geteuid() == 0 {
		waitKVM, err := resolveWaitKVM(opts.WaitKVM)
		if err != nil {
			return nil, err
		}
		opts.WaitKVM = waitKVM
		jailFolder = filepath.Base(waitKVM)
	}
	jailRoot := filepath.Join(chrootBase, jailFolder, opts.ID, "root")
	if err := os.MkdirAll(jailRoot, 0o755); err != nil {
		return nil, err
	}
	if err := copyFile(opts.Kernel, filepath.Join(jailRoot, "vmlinux")); err != nil {
		return nil, err
	}
	if err := copyFile(opts.Rootfs, filepath.Join(jailRoot, "rootfs.ext4")); err != nil {
		return nil, err
	}
	guestCID := opts.GuestCID
	if guestCID == 0 {
		guestCID = 3
	}
	if guestCID < 3 {
		return nil, fmt.Errorf("guest CID must be >= 3, got %d", guestCID)
	}
	cfg, err := fcConfigJSON(opts.BareExec, guestCID)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(jailRoot, "config.json"), cfg, 0o644); err != nil {
		return nil, err
	}

	uds := filepath.Join(jailRoot, "vsock.sock")
	_ = os.Remove(uds)
	_ = os.Remove(uds + "_" + strconv.Itoa(EventPort))

	ln, err := vsockhost.ListenGuestPort(uds, EventPort)
	if err != nil {
		return nil, fmt.Errorf("listen guest events: %w", err)
	}
	go acceptEvents(ln, events)

	w := &World{
		ID:         opts.ID,
		EventsPath: events,
		JailRoot:   jailRoot,
		UDS:        uds,
		GuestCID:   guestCID,
		eventLn:    ln,
		client: &http.Client{
			Timeout: 90 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return vsockhost.DialGuest(uds, ExecPort, 10*time.Second)
				},
			},
		},
	}

	cmd, engine, err := startVMM(opts, chrootBase, jailRoot)
	if err != nil {
		ln.Close()
		return nil, err
	}
	w.cmd = cmd
	w.Engine = engine

	// Host /dev/kvm bind over jailer's 0600 mknod: jailer-path only, fail-closed.
	// Unprivileged Firecracker fallback never mknods; do not race a goroutine there.
	if engine == "jailer" {
		kvmTarget := filepath.Join(jailRoot, "dev", "kvm")
		if err := overlayHostKvm(kvmTarget); err != nil {
			w.Stop()
			return nil, err
		}
		w.kvmOverlay = kvmTarget
	}

	if err := w.waitHealth(45 * time.Second); err != nil {
		w.Stop()
		return nil, err
	}
	return w, nil
}

func startVMM(opts StartOpts, chrootBase, jailRoot string) (*exec.Cmd, string, error) {
	logf, err := os.Create(filepath.Join(opts.WorkDir, "firecracker.log"))
	if err != nil {
		return nil, "", err
	}
	if os.Geteuid() == 0 {
		waitKVM, err := resolveWaitKVM(opts.WaitKVM)
		if err != nil {
			return nil, "", err
		}
		uid, gid, err := dropIDs(os.Getuid(), os.Getgid(), os.Getenv)
		if err != nil {
			return nil, "", err
		}
		realFC := filepath.Join(jailRoot, "firecracker.real")
		waitDst := filepath.Join(jailRoot, filepath.Base(waitKVM))
		if err := copyFile(opts.Firecracker, realFC); err != nil {
			return nil, "", fmt.Errorf("copy firecracker.real: %w", err)
		}
		if err := os.Chmod(realFC, 0o755); err != nil {
			return nil, "", err
		}
		if err := copyFile(waitKVM, waitDst); err != nil {
			return nil, "", fmt.Errorf("copy fc-waitkvm: %w", err)
		}
		if err := os.Chmod(waitDst, 0o755); err != nil {
			return nil, "", err
		}
		if err := chownTree(jailRoot, uid, gid); err != nil {
			return nil, "", err
		}
		cmd := exec.Command(opts.Jailer,
			"--id", opts.ID,
			"--exec-file", waitKVM,
			"--uid", strconv.Itoa(uid),
			"--gid", strconv.Itoa(gid),
			"--chroot-base-dir", chrootBase,
			"--cgroup-version", "2",
			"--parent-cgroup", "backlot-m2",
			"--",
			"--no-api",
			"--config-file", "config.json",
		)
		cmd.Stdout = logf
		cmd.Stderr = logf
		if err := cmd.Start(); err != nil {
			return nil, "", err
		}
		return cmd, "jailer", nil
	}
	cmd := exec.Command(opts.Firecracker, "--no-api", "--config-file", "config.json")
	cmd.Dir = jailRoot
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	return cmd, "firecracker", nil
}

func acceptEvents(ln net.Listener, eventsPath string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go drainEvents(c, eventsPath)
	}
}

func drainEvents(c net.Conn, eventsPath string) {
	defer c.Close()
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	f, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("open host jsonl: %v", err)
		return
	}
	defer f.Close()
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if _, err := f.Write(append(append([]byte{}, line...), '\n')); err != nil {
			log.Printf("append host jsonl: %v", err)
			return
		}
		_ = f.Sync()
	}
}

func (w *World) waitHealth(d time.Duration) error {
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://vsock/health", nil)
		resp, err := w.client.Do(req)
		cancel()
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
			last = fmt.Errorf("health %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("health timeout: %w", last)
}

type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func (w *World) Exec(ctx context.Context, argv []string, timeoutSec int) (*ExecResult, error) {
	return w.doExec(ctx, execURL(w.ID, false), argv, timeoutSec)
}

// BareExec posts to /v1/internal/bare-exec (guest must have been booted with
// backlot.bare_exec=1). Used only by m2test to drive in-guest run_int.py.
func (w *World) BareExec(ctx context.Context, argv []string, timeoutSec int) (*ExecResult, error) {
	return w.doExec(ctx, execURL(w.ID, true), argv, timeoutSec)
}

func execURL(worldID string, bareExec bool) string {
	if bareExec {
		return "http://vsock/v1/internal/bare-exec"
	}
	return "http://vsock/v1/worlds/" + worldID + "/exec"
}

func execBody(argv []string, timeoutSec int) []byte {
	body, _ := json.Marshal(map[string]any{"argv": argv, "timeout": timeoutSec})
	return body
}

func (w *World) doExec(ctx context.Context, url string, argv []string, timeoutSec int) (*ExecResult, error) {
	body := execBody(argv, timeoutSec)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("exec http %d: %s", resp.StatusCode, raw)
	}
	var out ExecResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (w *World) CmdPid() int {
	if w.cmd != nil && w.cmd.Process != nil {
		return w.cmd.Process.Pid
	}
	return 0
}

func (w *World) Stop() {
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			_, _ = w.cmd.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = w.cmd.Process.Kill()
			_, _ = w.cmd.Process.Wait()
		}
	}
	if w.eventLn != nil {
		_ = w.eventLn.Close()
	}
	unmountKvmOverlay(w.kvmOverlay)
}

func resolveWaitKVM(explicit string) (string, error) {
	waitKVM := explicit
	if waitKVM == "" {
		waitKVM = os.Getenv("BACKLOT_FC_WAITKVM")
	}
	if waitKVM == "" {
		return "", fmt.Errorf("WaitKVM required for jailer (set StartOpts.WaitKVM or BACKLOT_FC_WAITKVM)")
	}
	// Match jailer's canonicalize so <base>/<basename>/<id>/root agrees.
	if resolved, err := filepath.EvalSymlinks(waitKVM); err == nil {
		waitKVM = resolved
	}
	if _, err := os.Stat(waitKVM); err != nil {
		return "", fmt.Errorf("WaitKVM %s: %w", waitKVM, err)
	}
	return waitKVM, nil
}

// dropIDs is the uid/gid the VMM runs as after the privileged starter
// unshares. Passing 0 makes the jailed KVM node unusable on this host
// (EACCES). sudo/pkexec must export SUDO_UID/PKEXEC_UID (and preferably
// SUDO_GID/PKEXEC_GID).
func dropIDs(uid, gid int, getenv func(string) string) (int, int, error) {
	if uid != 0 {
		return uid, gid, nil
	}
	if v := getenv("SUDO_UID"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, 0, fmt.Errorf("SUDO_UID: %w", err)
		}
		uid = n
	} else if v := getenv("PKEXEC_UID"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, 0, fmt.Errorf("PKEXEC_UID: %w", err)
		}
		uid = n
	}
	if v := getenv("SUDO_GID"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, 0, fmt.Errorf("SUDO_GID: %w", err)
		}
		gid = n
	} else if v := getenv("PKEXEC_GID"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, 0, fmt.Errorf("PKEXEC_GID: %w", err)
		}
		gid = n
	} else if gid == 0 && uid != 0 {
		gid = uid
	}
	if uid == 0 {
		return 0, 0, fmt.Errorf("privileged start with uid 0 cannot open KVM; invoke via sudo or pkexec so SUDO_UID/PKEXEC_UID is set")
	}
	return uid, gid, nil
}

func chownTree(root string, uid, gid int) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(p, uid, gid)
	})
}

// overlayHostKvm waits for jailer to mknod target, then bind-mounts host
// /dev/kvm over it. Fail-closed: returns an error if the node never appears
// or Mount fails (errno is logged).
func overlayHostKvm(target string) error {
	deadline := time.Now().Add(3 * time.Second)
	var lastStat error
	for time.Now().Before(deadline) {
		if _, err := os.Stat(target); err != nil {
			lastStat = err
			time.Sleep(time.Millisecond)
			continue
		}
		if err := syscall.Mount("/dev/kvm", target, "", syscall.MS_BIND, ""); err != nil {
			log.Printf("overlayHostKvm: mount /dev/kvm -> %s: %v", target, err)
			return fmt.Errorf("bind /dev/kvm over %s: %w", target, err)
		}
		return nil
	}
	return fmt.Errorf("overlayHostKvm: %s never appeared: %v", target, lastStat)
}

func unmountKvmOverlay(target string) {
	if target == "" {
		return
	}
	if err := syscall.Unmount(target, syscall.MNT_DETACH); err != nil {
		log.Printf("unmountKvmOverlay: MNT_DETACH %s: %v", target, err)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

const defaultBootArgs = "console=ttyS0 reboot=k panic=1 pci=off nomodules random.trust_cpu=on init=/sbin/init root=/dev/vda rw"

func fcConfigJSON(bareExec bool, guestCID uint32) ([]byte, error) {
	bootArgs := defaultBootArgs
	if bareExec {
		bootArgs = bootArgs + " backlot.bare_exec=1"
	}
	cfg := map[string]any{
		"boot-source": map[string]any{
			"kernel_image_path": "vmlinux",
			"boot_args":         bootArgs,
		},
		"drives": []any{
			map[string]any{
				"drive_id":       "rootfs",
				"path_on_host":   "rootfs.ext4",
				"is_root_device": true,
				"is_read_only":   false,
			},
		},
		"machine-config": map[string]any{
			"vcpu_count":   2,
			"mem_size_mib": 512,
			"smt":          false,
		},
		"vsock": map[string]any{
			"guest_cid": guestCID,
			"uds_path":  "vsock.sock",
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}
