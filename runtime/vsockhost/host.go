// Package vsockhost talks to Firecracker's host-side vsock Unix sockets.
//
// Host→guest: dial uds_path, send "CONNECT <port>\n", read "OK ...\n", then bytes.
// Guest→host: listen on uds_path_<port> (Firecracker forwards guest AF_VSOCK).
package vsockhost

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// DialGuest connects to a port the guest is listening on (Firecracker CONNECT).
func DialGuest(udsPath string, port int, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{Timeout: timeout}
	c, err := d.Dial("unix", udsPath)
	if err != nil {
		return nil, err
	}
	if err := c.SetDeadline(time.Now().Add(timeout)); err != nil {
		c.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(c, "CONNECT %d\n", port); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("vsock CONNECT ack: %w", err)
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "OK ") {
		c.Close()
		return nil, fmt.Errorf("vsock CONNECT rejected: %q", line)
	}
	if err := c.SetDeadline(time.Time{}); err != nil {
		c.Close()
		return nil, err
	}
	return &prefixConn{Conn: c, r: io.MultiReader(br, c)}, nil
}

type prefixConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// ListenGuestPort listens for guest-initiated connections to HOST_CID:port.
// Firecracker expects the Unix socket at udsPath+"_"+strconv.Itoa(port).
func ListenGuestPort(udsPath string, port int) (net.Listener, error) {
	path := udsPath + "_" + strconv.Itoa(port)
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	return ln, nil
}
