package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/PixnBits/backlot/router"
)

func main() {
	addr := env("ROUTER_ADDR", ":8080")
	deskURL := env("DESK_URL", "http://desk:8090")
	lotBoss := strings.TrimSpace(os.Getenv("LOT_BOSS_URL"))

	var engine router.Engine = router.UnavailableEngine{}
	if lotBoss != "" {
		engine = router.NewHTTPEngine(strings.TrimRight(lotBoss, "/"))
	}

	srv := router.NewServer(engine, deskURL)
	s := &http.Server{
		Addr:              addr,
		Handler:           srv.Mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("router listening on %s desk=%s lot_boss=%q", addr, deskURL, lotBoss)
	log.Fatal(s.ListenAndServe())
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
