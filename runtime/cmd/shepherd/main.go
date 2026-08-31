package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/PixnBits/backlot/runtime/world"
)

func main() {
	log.SetPrefix("shepherd ")
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)

	id := flag.String("id", "demo", "world id")
	work := flag.String("work", "", "host work directory")
	kernel := flag.String("kernel", "", "vmlinux path")
	rootfs := flag.String("rootfs", "", "rootfs.ext4 path")
	fcBin := flag.String("firecracker", os.Getenv("FIRECRACKER_BIN"), "firecracker binary")
	jailerBin := flag.String("jailer", os.Getenv("JAILER_BIN"), "jailer binary")
	flag.Parse()
	if *work == "" || *kernel == "" || *rootfs == "" {
		log.Fatal("need -work -kernel -rootfs")
	}
	if *fcBin == "" {
		*fcBin = "/usr/local/firecracker/v1.15.1/firecracker"
	}
	if *jailerBin == "" {
		*jailerBin = "/usr/local/firecracker/v1.15.1/jailer"
	}

	w, err := world.Start(world.StartOpts{
		ID:          *id,
		WorkDir:     *work,
		Kernel:      *kernel,
		Rootfs:      *rootfs,
		Firecracker: *fcBin,
		Jailer:      *jailerBin,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer w.Stop()
	log.Printf("world %s engine=%s events=%s", w.ID, w.Engine, w.EventsPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
