// fc-waitkvm is the jailer --exec-file for Backlot worlds.
//
// Jailer mknods a fresh /dev/kvm (0600) then execs this binary. The host
// bind-mounts the real /dev/kvm over that node (ACL/group). We must not
// exec Firecracker until that bind is live — otherwise FC exits with
// KVM EACCES and vsock never appears (health timeout / connection refused).
package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

const realFC = "/firecracker.real"

func main() {
	deadline := time.Now().Add(10 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
		if err == nil {
			_ = f.Close()
			last = nil
			break
		}
		last = err
		time.Sleep(time.Millisecond)
	}
	if last != nil {
		fmt.Fprintf(os.Stderr, "fc-waitkvm: /dev/kvm never opened: %v\n", last)
		os.Exit(1)
	}
	args := append([]string{realFC}, os.Args[1:]...)
	if err := syscall.Exec(realFC, args, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "fc-waitkvm: exec %s: %v\n", realFC, err)
		os.Exit(1)
	}
}
