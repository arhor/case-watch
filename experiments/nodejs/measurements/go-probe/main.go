// Command probe wires the unchanged Go adapters to synthetic benchmark endpoints.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/arhor/case-watch/internal/clients"
	"github.com/arhor/case-watch/internal/database"
	"github.com/arhor/case-watch/internal/database/repositories"
	"github.com/arhor/case-watch/internal/entrypoints/schedule"
	"github.com/arhor/case-watch/internal/entrypoints/telegram"
	"github.com/arhor/case-watch/internal/services"
	"github.com/arhor/case-watch/internal/utils"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "probe_failed")
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	db, err := database.Open(ctx, "/data/bench.db")
	if err != nil {
		return err
	}
	defer db.Close()
	store := repositories.NewStore(db)
	err = store.Enrollments.Create(ctx, 1, "00000000000000000000000000000001", "synthetic-number", "synthetic-password", "Synthetic stage", time.Now().Add(-2*time.Minute))
	if err != nil {
		return err
	}
	clock := utils.NewClock()
	portal := clients.NewGdanskPortalClient(clients.GdanskPortalClientOptions{BaseURL: os.Getenv("BENCH_ENDPOINT"), Clock: clock})
	defer portal.Close()
	cases := services.NewCaseService(services.CaseServiceConfig{Store: store, Portal: portal, Clock: clock, OnStorageError: stop})
	defer cases.Shutdown()
	bot, err := telegram.New(ctx, telegram.Config{Token: "123:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Endpoint: os.Getenv("BENCH_ENDPOINT"), CaseService: cases, Clock: clock, Diagnostic: func(category string) { fmt.Fprintln(os.Stderr, category) }})
	if err != nil {
		return err
	}
	defer bot.Close()
	jobs, err := schedule.New(schedule.Config{CaseService: cases, Clock: clock})
	if err != nil {
		return err
	}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() { defer workers.Done(); _ = bot.Run(ctx) }()
	go func() { defer workers.Done(); _ = jobs.Run(ctx) }()
	if os.Getenv("BENCH_SCENARIO") == "scheduled" {
		workers.Add(1)
		go func() {
			defer workers.Done()
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			owners, err := cases.ScheduledOwners(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "inventory_failed")
				return
			}
			for _, owner := range owners {
				if _, err := cases.Scheduled(ctx, owner); err != nil {
					fmt.Fprintln(os.Stderr, "check_failed")
				}
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, os.Getenv("BENCH_ENDPOINT")+"/scheduled-done", nil)
			if err != nil {
				return
			}
			response, err := http.DefaultClient.Do(request)
			if err == nil {
				response.Body.Close()
			}
		}()
	}
	<-ctx.Done()
	cases.Shutdown()
	workers.Wait()
	return nil
}
