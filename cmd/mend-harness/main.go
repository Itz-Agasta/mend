// Command mend-harness runs an incident scenario against the demo shop and
// reports how far each recovery strategy lands from ground truth.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/Itz-Agasta/mend/internal/harness"
)

const defaultPGURL = "postgres://mend:mend@localhost:55432/postgres?sslmode=disable"

func main() {
	s := harness.PricingBot()
	pgURL := flag.String("pg", envOr("MEND_PG_URL", defaultPGURL), "admin URL of a scratch Postgres server (databases mend_h_* are recreated)")
	jsonOut := flag.String("json", "", "also write the report as JSON to this file")
	flag.IntVar(&s.Ops, "ops", s.Ops, "number of client requests")
	flag.IntVar(&s.BadAt, "bad-at", s.BadAt, "request index before which the bad action runs")
	flag.Uint64Var(&s.Seed, "seed", s.Seed, "workload seed")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	r, err := harness.Run(ctx, *pgURL, s)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mend-harness:", err)
		os.Exit(1)
	}
	if err := r.WriteText(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mend-harness:", err)
		os.Exit(1)
	}
	if *jsonOut != "" {
		js, err := json.MarshalIndent(r, "", "  ")
		if err == nil {
			err = os.MkdirAll(filepath.Dir(*jsonOut), 0o755)
		}
		if err == nil {
			err = os.WriteFile(*jsonOut, append(js, '\n'), 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "mend-harness:", err)
			os.Exit(1)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
