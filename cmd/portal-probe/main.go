package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/arhor/case-watch/internal/portal"
	"os"
	"time"
)

func main() {
	live := flag.Bool("live", false, "allow one bounded live portal attempt")
	flag.Parse()
	n, p := os.Getenv("CASE_WATCH_CASE_NUMBER"), os.Getenv("CASE_WATCH_PASSWORD")
	if !*live || n == "" || p == "" {
		fmt.Fprintln(os.Stderr, "requires --live and CASE_WATCH_CASE_NUMBER / CASE_WATCH_PASSWORD")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, err := portal.Probe(ctx, "https://klient.gdansk.uw.gov.pl", n, p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v (requests: %d)\n", err, r.Requests)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(r)
}
