package application

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"

	"github.com/arhor/case-watch/internal/bot"
	"github.com/arhor/case-watch/internal/config"
	"github.com/arhor/case-watch/internal/database"
	"github.com/arhor/case-watch/internal/database/store"
	"github.com/arhor/case-watch/internal/scheduler"
	"github.com/arhor/case-watch/internal/services/cases"
	"github.com/arhor/case-watch/internal/services/cases/portal"
	"github.com/arhor/case-watch/internal/utils"
)

var (
	errStartup = errors.New("service startup validation failed")
	errService = errors.New("service operation failed")
)

func Run(ctx context.Context) error {
	return run(ctx)
}

func run(ctx context.Context) (result error) {
	clock := utils.NewClock()

	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	db, err := database.Open(ctx, cfg.DBPath)
	if err != nil {
		return errStartup
	}
	defer func() {
		if err := db.Close(); err != nil {
			result = errors.Join(result, errService)
		}
	}()

	storage := store.New(db)

	work, stop := context.WithCancelCause(ctx)

	defer stop(nil)

	fail := func() { stop(errService) }

	diagnostic := func(category string) {
		log.Print(category)
		if category == "telegram_invalid_token" || category == "inventory_failed" {
			fail()
		}
	}

	portalClient := portal.NewClient(portal.Options{Clock: clock})

	caseService := cases.New(cases.Config{
		DB:             storage,
		Portal:         portalClient,
		Clock:          clock,
		OnStorageError: fail,
	})
	defer caseService.Shutdown()

	telegramBot, err := bot.New(work, bot.Config{
		Token:       cfg.TelegramBotToken,
		CaseService: caseService,
		Clock:       clock,
		Diagnostic:  diagnostic,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errStartup
	}
	defer telegramBot.Close()

	scheduled, err := scheduler.New(scheduler.Config{
		Cases:  caseService,
		Clock:  clock,
		Report: diagnostic,
	})
	if err != nil {
		return errStartup
	}

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
