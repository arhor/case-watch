package application

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"

	"github.com/arhor/case-watch/internal/adapters/inbound/schedule"
	"github.com/arhor/case-watch/internal/adapters/inbound/telegram"
	"github.com/arhor/case-watch/internal/adapters/outbound/clients"
	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/platform/clock"
	"github.com/arhor/case-watch/internal/platform/guard"
	"github.com/arhor/case-watch/internal/services"
)

func Run(ctx context.Context) error {
	return run(ctx)
}

// -- Internals ----------------------------------------------------------------

var errService = errors.New("service operation failed")

func run(ctx context.Context) (result error) {
	clock := clock.NewClock()

	cfg := guard.Must(loadConfig(os.LookupEnv))

	db, err := database.Open(ctx, cfg.DBPath)
	if err != nil && ctx.Err() != nil {
		return nil
	}
	db = guard.Must(db, err)
	defer func() {
		if err := db.Close(); err != nil {
			result = errors.Join(result, errService)
		}
	}()

	store := repositories.NewStore(db)

	work, stop := context.WithCancelCause(ctx)

	defer stop(nil)

	fail := func() { stop(errService) }

	diagnostic := func(category string) {
		log.Print(category)
		if category == "telegram_invalid_token" || category == "inventory_failed" {
			fail()
		}
	}

	portalClient := clients.NewGdanskPortalClient(clients.GdanskPortalClientOptions{Clock: clock})
	// Deferred before caseService.Shutdown so it runs after in-flight checks drain.
	defer portalClient.Close()

	caseService := services.NewCaseService(services.CaseServiceConfig{
		Store:          store,
		Portal:         portalClient,
		Clock:          clock,
		OnStorageError: fail,
	})
	defer caseService.Shutdown()

	telegramBot, err := telegram.New(work, telegram.Config{
		Token:       cfg.TelegramBotToken,
		CaseService: caseService,
		Clock:       clock,
		Diagnostic:  diagnostic,
	})
	if err != nil && ctx.Err() != nil {
		return nil
	}
	telegramBot = guard.Must(telegramBot, err)
	defer telegramBot.Close()

	scheduled := schedule.New(schedule.Config{
		CaseService: caseService,
		Clock:       clock,
		Report:      diagnostic,
	})

	if ctx.Err() != nil {
		return nil
	}

	if work.Err() == nil {
		var running sync.WaitGroup
		start := func(loop func(context.Context) error) {
			running.Go(func() {
				_ = loop(work)
				if work.Err() == nil {
					fail()
				}
			})
		}
		start(telegramBot.Run)
		start(scheduled.Run)
		<-work.Done()
		caseService.Shutdown()
		running.Wait()
	}

	if errors.Is(context.Cause(work), errService) {
		return errService
	}
	return nil
}
