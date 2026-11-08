// Package application constructs the case service, bot and scheduler and runs them.
package application

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/bot"
	"github.com/arhor/case-watch/internal/config"
	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/repositories"
	"github.com/arhor/case-watch/internal/scheduler"
	"github.com/arhor/case-watch/internal/services/cases"
	"github.com/arhor/case-watch/internal/services/cases/portal"
)

type clock struct{}

func (clock) Now() time.Time { return time.Now() }

// Only external boundaries are replaceable in offline application tests.
type telegramBot interface {
	Validate(context.Context) error
	Run(context.Context) error
	Close()
}
type dependencies struct {
	lookup   func(string) (string, bool)
	report   func(string)
	portal   func() domain.Portal
	telegram func(bot.Config) (telegramBot, error)
}

func Run(ctx context.Context) error {
	return run(ctx, dependencies{lookup: os.LookupEnv})
}

func run(ctx context.Context, d dependencies) (result error) {
	cfg, err := config.Load(d.lookup)
	if err != nil {
		return err
	}
	path, err := databasePath(cfg.DBPath)
	if err != nil {
		return err
	}
	lock, err := acquire(path)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.close()) }()
	if err := databaseFile(path); err != nil {
		return err
	}
	db, err := repositories.NewStore(ctx, path)
	if err != nil {
		return errStartup
	}
	defer func() {
		if err := db.Close(); err != nil {
			result = errors.Join(result, errService)
		}
	}()

	work, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	fail := func() { stop(errService) }
	report := d.report
	if report == nil {
		report = func(category string) { log.Print(category) }
	}
	diagnostic := func(category string) {
		report(category)
		if category == "telegram_invalid_token" || category == "inventory_failed" {
			fail()
		}
	}
	clock := clock{}
	var portalClient domain.Portal = portal.NewClient(portal.Options{Now: clock.Now})
	if d.portal != nil {
		portalClient = d.portal()
	}
	caseService := cases.New(cases.Config{DB: db, Portal: portalClient, Clock: clock, OnStorageError: fail})
	defer caseService.Shutdown()
	newBot := d.telegram
	if newBot == nil {
		newBot = func(c bot.Config) (telegramBot, error) { return bot.New(c) }
	}
	telegramBot, err := newBot(bot.Config{Token: cfg.TelegramBotToken, Cases: caseService, Clock: clock, Diagnostic: diagnostic})
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
	if err := telegramBot.Validate(work); err != nil {
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
