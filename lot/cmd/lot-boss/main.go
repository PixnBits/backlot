package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/PixnBits/backlot/lot"
)

func main() {
	addr := flag.String("addr", ":8070", "listen address")
	max := flag.Int("max-worlds", lot.DefaultMaxWorlds, "cap live worlds on this node")
	warm := flag.Int("warm-pool", lot.DefaultWarmPool, "warm pool size (0 or 1)")
	flag.Parse()

	boss := lot.NewBoss(*max, *warm, true)
	s := &http.Server{
		Addr:              *addr,
		Handler:           boss.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("lot-boss listening on %s %s (Phase 1 stub: leases return 503 until Phase 2)", *addr, lot.CapString(boss))
	log.Fatal(s.ListenAndServe())
}
