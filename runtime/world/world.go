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
}

type World struct {
	ID         string
	EventsPath string
	JailRoot   string
	UDS        string
	Engine     string // "jailer" or "firecracker"
	cmd        *exec.Cmd
	eventLn    net.Listener
	client     *http.Client
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
	jailRoot := filepath.Join(chrootBase, "firecracker", opts.ID, "root")
	if err := os.MkdirAll(jailRoot, 0o755); err != nil {
		return nil, err
	}
	if err := copyFile(opts.Kernel, filepath.Join(jailRoot, "vmlinux")); err != nil {
		return nil, err
	}
	if err := copyFile(opts.Rootfs, filepath.Join(jailRoot, "rootfs.ext4")); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(jailRoot, "config.json"), []byte(fcConfig), 0o644); err != nil {
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
		cmd := exec.Command(opts.Jailer,
			"--id", opts.ID,
			"--exec-file", opts.Firecracker,
			"--uid", strconv.Itoa(os.Getuid()),
			"--gid", strconv.Itoa(os.Getgid()),
			"--chroot-base-dir", chrootBase,
			"--cgroup-version", "2",
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
	return w.ExecOpt(ctx, argv, timeoutSec, true)
}

func (w *World) ExecOpt(ctx context.Context, argv []string, timeoutSec int, jail bool) (*ExecResult, error) {
	body, _ := json.Marshal(map[string]any{"argv": argv, "timeout": timeoutSec, "jail": jail})
	url := "http://vsock/v1/worlds/" + w.ID + "/exec"
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

const fcConfig = `{
  "boot-source": {
    "kernel_image_path": "vmlinux",
    "boot_args": "console=ttyS0 reboot=k panic=1 pci=off nomodules random.trust_cpu=on init=/sbin/init root=/dev/vda rw"
  },
  "drives": [
    {
      "drive_id": "rootfs",
      "path_on_host": "rootfs.ext4",
      "is_root_device": true,
      "is_read_only": false
    }
  ],
  "machine-config": {
    "vcpu_count": 2,
    "mem_size_mib": 512,
    "smt": false
  },
  "vsock": {
    "guest_cid": 3,
    "uds_path": "vsock.sock"
  }
}
`
