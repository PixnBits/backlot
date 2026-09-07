package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/PixnBits/backlot/desk"
)

func main() {
	addr := env("DESK_ADDR", ":8090")
	data := env("DESK_DATA", "/data")
	store, err := desk.NewStore(data)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	srv := desk.NewServer(store)
	s := &http.Server{
		Addr:              addr,
		Handler:           srv.Mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("desk listening on %s data=%s", addr, data)
	log.Fatal(s.ListenAndServe())
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
