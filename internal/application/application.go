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
	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/scheduler"
	"github.com/arhor/case-watch/internal/services/cases"
	"github.com/arhor/case-watch/internal/services/cases/portal"
	"github.com/arhor/case-watch/internal/utils"
)

var (
	errStartup = errors.New("service startup validation failed")
	errService = errors.New("service operation failed")
)

// Only external boundaries are replaceable in offline application tests.
type telegramClient interface {
	bot.Transport
	Validate(context.Context) error
}
type dependencies struct {
	lookup   func(string) (string, bool)
	portal   func() domain.Portal
	telegram func(bot.ClientConfig) (telegramClient, error)
}

func Run(ctx context.Context) error {
	return run(ctx, dependencies{lookup: os.LookupEnv})
}

func run(ctx context.Context, d dependencies) (result error) {

	clock := utils.NewClock()

	cfg, err := config.Load(d.lookup)
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
	var portalClient domain.Portal = portal.NewClient(portal.Options{Now: clock.Now})
	if d.portal != nil {
		portalClient = d.portal()
	}
	caseService := cases.New(cases.Config{DB: storage, Portal: portalClient, Clock: clock, OnStorageError: fail})
	defer caseService.Shutdown()
	newClient := d.telegram
	if newClient == nil {
		newClient = func(c bot.ClientConfig) (telegramClient, error) { return bot.NewClient(c) }
	}
	client, err := newClient(bot.ClientConfig{Token: cfg.TelegramBotToken, Diagnostic: diagnostic})
	if err != nil {
		return errStartup
	}
	telegramBot, err := bot.New(bot.Config{Client: client, Cases: caseService, Clock: clock, Diagnostic: diagnostic})
	if err != nil {
		return errStartup
	}
	defer telegramBot.Close()
	scheduled, err := scheduler.New(scheduler.Config{Cases: caseService, Clock: clock, Report: diagnostic})
	if err != nil {
		return errStartup
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := client.Validate(work); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errStartup
	}
	if work.Err() == nil {
		var running sync.WaitGroup
		start := func(loop func(context.Context) error) {
			running.Add(1)
			go func() {
				defer running.Done()
				_ = loop(work)
				if work.Err() == nil {
					fail()
				}
			}()
		}
		start(telegramBot.Run)
		start(scheduled.Run)
		<-work.Done()
		// Cancel shared portal work before joining callers waiting for it.
		caseService.Shutdown()
		running.Wait()
	}
	if errors.Is(context.Cause(work), errService) {
		return errService
	}
	return nil
}
