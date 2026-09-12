package main

// Userspace egress proxy for Backlot Phase 3.
// Default deny. Only hosts in EGRESS_ALLOW (comma-separated) may CONNECT or be proxied.
// Guests still have no NIC; this is the only future egress path (host-side).

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func allowlist() map[string]bool {
	out := map[string]bool{}
	for _, h := range strings.Split(os.Getenv("EGRESS_ALLOW"), ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		h = strings.Split(h, ":")[0]
		if h != "" {
			out[h] = true
		}
	}
	return out
}

func hostOf(raw string) string {
	h := strings.ToLower(raw)
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	h = strings.Split(h, "/")[0]
	return strings.Split(h, ":")[0]
}

func main() {
	addr := os.Getenv("LISTEN")
	if addr == "" {
		addr = ":3128"
	}
	allow := allowlist()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "service": "egress-proxy", "allow": len(allow)})
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           &proxy{allow: allow, next: mux},
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("egress-proxy listen %s allow=%d", addr, len(allow))
	log.Fatal(srv.ListenAndServe())
}

type proxy struct {
	allow map[string]bool
	next  http.Handler
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" && r.Method != http.MethodConnect {
		p.next.ServeHTTP(w, r)
		return
	}
	host := hostOf(r.Host)
	if r.Method == http.MethodConnect {
		host = hostOf(r.RequestURI)
		if host == "" {
			host = hostOf(r.Host)
		}
	}
	if !p.allow[host] {
		log.Printf("deny %s %s host=%s", r.Method, r.URL, host)
		http.Error(w, "egress denied", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodConnect {
		hijackCONNECT(w, r)
		return
	}
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func hijackCONNECT(w http.ResponseWriter, r *http.Request) {
	dst := r.Host
	if !strings.Contains(dst, ":") {
		dst += ":443"
	}
	back, err := net.DialTimeout("tcp", dst, 10*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = back.Close()
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, bufrw, err := hj.Hijack()
	if err != nil {
		_ = back.Close()
		return
	}
	_, _ = io.WriteString(bufrw, "HTTP/1.1 200 Connection Established\r\n\r\n")
	_ = bufrw.Flush()
	go func() { _, _ = io.Copy(back, bufrw); _ = back.Close() }()
	_, _ = io.Copy(client, back)
	_ = client.Close()
	_ = back.Close()
}
