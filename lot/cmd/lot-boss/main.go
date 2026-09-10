package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/PixnBits/backlot/lot"
)

func main() {
	addr := flag.String("addr", ":8070", "listen address")
	max := flag.Int("max-worlds", lot.DefaultMaxWorlds, "cap live worlds on this node")
	warm := flag.Int("warm-pool", lot.DefaultWarmPool, "warm pool size (0 or 1)")
	work := flag.String("work", "", "host work directory for jail roots")
	kernel := flag.String("kernel", "", "vmlinux path")
	rootfs := flag.String("rootfs", "", "rootfs.ext4 path")
	desk := flag.String("desk", env("DESK_URL", "http://127.0.0.1:8090"), "continuity desk base URL")
	requireJailer := flag.Bool("require-jailer", true, "refuse leases unless euid=0 (jailer path)")
	flag.Parse()

	root := repoRoot()
	if *work == "" {
		*work = filepath.Join(os.TempDir(), "backlot-lot")
	}
	if *kernel == "" {
		*kernel = filepath.Join(root, "guest/artifacts/vmlinux-6.1.102")
	}
	if *rootfs == "" {
		*rootfs = filepath.Join(root, "guest/artifacts/rootfs.ext4")
	}
	_ = os.MkdirAll(*work, 0o700)

	boss := lot.NewBossConfig(lot.Config{
		MaxWorlds:     *max,
		WarmPool:      *warm,
		RequireKVM:    true,
		RequireJailer: *requireJailer,
		WorkDir:       *work,
		Kernel:        *kernel,
		Rootfs:        *rootfs,
		DeskURL:       *desk,
	})

	s := &http.Server{
		Addr:              *addr,
		Handler:           boss.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		log.Printf("lot-boss shutting down; stopping worlds")
		boss.StopAll()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = s.Shutdown(shutdownCtx)
	}()

	log.Printf("lot-boss listening on %s %s engine_ready=%v desk=%s work=%s",
		*addr, lot.CapString(boss), boss.EngineReady(), *desk, *work)
	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
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
