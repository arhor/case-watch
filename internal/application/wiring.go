package application

import (
	"log"
	"time"

	"github.com/arhor/case-watch/internal/config"
	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/entrypoints/scheduler"
	"github.com/arhor/case-watch/internal/entrypoints/telegram"
	"github.com/arhor/case-watch/internal/repositories"
	"github.com/arhor/case-watch/internal/repositories/portal"
	telegramrepository "github.com/arhor/case-watch/internal/repositories/telegram"
	"github.com/arhor/case-watch/internal/services/replies"
	"github.com/arhor/case-watch/internal/services/tracking"
)

type clock struct{}

func (clock) Now() time.Time { return time.Now() }

// wire constructs the complete graph without starting any runtime loops or
// contacting Telegram/the portal. Assign resources as acquired so close also
// handles every partially constructed graph.
func wire(r *runtime, c config.Config, d dependencies) error {
	db, err := databasePath(c.DBPath)
	if err != nil {
		return err
	}
	r.lock, err = acquire(db)
	if err != nil {
		return err
	}
	if err := databaseFile(db, true); err != nil {
		return err
	}
	r.store, err = repositories.NewStore(r.work, db)
	if err != nil {
		return errStartup
	}

	clock := clock{}
	p := domain.Portal(portal.NewClient(portal.Options{Now: clock.Now}))
	if d.portal != nil {
		p = d.portal()
	}
	r.tracking = tracking.New(tracking.Persistence{DB: r.store}, p, clock)
	monitored := monitoredTracking{r.tracking, r.fail}
	report := d.report
	if report == nil {
		report = func(category string) { log.Print(category) }
	}
	newClient := d.telegram
	if newClient == nil {
		newClient = func(c telegramrepository.Config) (telegramClient, error) {
			return telegramrepository.New(c)
		}
	}
	r.client, err = newClient(telegramrepository.Config{
		Token: c.TelegramBotToken,
		Diagnostic: func(category string) {
			if category == "telegram_invalid_token" {
				r.fail()
			}
			report(category)
		},
	})
	if err != nil {
		return errStartup
	}
	sender, err := replies.New(replies.Config{
		Clock: clock, Sender: r.client,
		Report: func(category string) {
			report(category)
			if category == string(domain.SendInvalidToken) {
				r.fail()
			}
		},
	})
	if err != nil {
		return errStartup
	}
	telegramBot, err := telegram.New(telegram.Config{
		Client: r.client, Tracking: monitored, Clock: clock,
		Diagnostic: report,
		DirectSend: sender.Send,
	})
	if err != nil {
		return errStartup
	}
	r.telegramBot = telegramBot
	r.scheduler, err = scheduler.New(scheduler.Config{
		Tracking: monitored, Clock: clock,
		Report: func(category string) {
			report(category)
			if category == "inventory_failed" {
				r.fail()
			}
		},
	})
	if err != nil {
		return errStartup
	}
	return nil
}
