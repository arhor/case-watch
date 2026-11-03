// Package service owns process lifetime and offline-only injection boundaries.
package service

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/auth"
	"github.com/arhor/case-watch/internal/config"
	"github.com/arhor/case-watch/internal/credentials"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/notifications"
	"github.com/arhor/case-watch/internal/portal"
	"github.com/arhor/case-watch/internal/scheduler"
	"github.com/arhor/case-watch/internal/telegram"
	"github.com/arhor/case-watch/internal/tracking"
)

type clock struct{}

func (clock) Now() time.Time { return time.Now() }

type Transport interface {
	application.Sender
	Validate(context.Context) error
	Start(context.Context)
	Close()
}

// Factories are programmatic test injection only; runtime exposes no endpoint env.
type Factories struct {
	Report   func(string)
	Portal   func() application.Portal
	Telegram func(telegram.Config) (Transport, error)
}

func Verify(ctx context.Context, s *data.Store, k *credentials.Keyring) error {
	rows, e := s.Enrollments.FindAll(ctx)
	if e != nil {
		return ErrStartup
	}
	for _, row := range rows {
		v, e := k.Open(row.Credentials)
		v = application.Credentials{}
		_ = v
		if e != nil {
			return ErrStartup
		}
	}
	return nil
}

// Run validates configuration, key separation, permissions, schema and every
// credential before constructing transports. It holds the lock through DB close.
func Run(ctx context.Context, lookup func(string) (string, bool), f Factories) (result error) {
	c, e := config.Load(lookup)
	if e != nil {
		return e
	}
	db, key, e := Paths(c.DBPath, c.KeyFile)
	if e != nil {
		return e
	}
	k, e := credentials.Load(key)
	if e != nil {
		return ErrStartup
	}
	lock, e := Acquire(db)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = databaseFile(db, true); e != nil {
		return e
	}
	s, e := data.Open(ctx, db)
	if e != nil {
		return ErrStartup
	}
	defer func() {
		if s.Close() != nil {
			result = ErrService
		}
	}()
	if e = Verify(ctx, s, k); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return nil
	}
	policy := auth.New(c.AllowAllUsers, c.AllowedUserIDs)
	p := application.Portal(portal.NewClient(portal.Options{}))
	if f.Portal != nil {
		p = f.Portal()
	}
	coord := tracking.New(tracking.Persistence{DB: s}, k, p, policy, clock{})
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	var worker *notifications.Worker
	var failOnce sync.Once
	fatal := make(chan struct{})
	fail := func() { failOnce.Do(func() { close(fatal); cancel() }) }
	report := f.Report
	if report == nil {
		report = func(category string) { log.Print(category) }
	}
	monitored := monitoredTracking{coord, fail}
	tc := telegram.Config{Token: c.TelegramBotToken, Policy: policy, Tracking: monitored, Store: monitoredReader{s, fail}, Clock: clock{}}
	tc.Membership = func(ctx context.Context, m telegram.Membership) {
		worker.Membership(ctx, notifications.Event{Owner: m.Owner, Blocked: m.Blocked, Unblocked: m.Unblocked, Interaction: m.Interaction})
	}
	tc.DirectSend = func(ctx context.Context, id int64, text string) (application.SendOutcome, error) {
		out, e := worker.SendDirect(ctx, id, text)
		if errors.Is(e, notifications.ErrWorker) {
			fail()
		}
		return out, e
	}
	tc.Diagnostic = func(category string) {
		if category == "telegram_invalid_token" {
			fail()
		}
		report(category)
	}
	newTransport := f.Telegram
	if newTransport == nil {
		newTransport = func(c telegram.Config) (Transport, error) { return telegram.New(c) }
	}
	transport, e := newTransport(tc)
	if e != nil {
		coord.Shutdown()
		return ErrStartup
	}
	if e = transport.Validate(work); e != nil {
		transport.Close()
		coord.Shutdown()
		if ctx.Err() != nil {
			return nil
		}
		return ErrStartup
	}
	worker, e = notifications.New(notifications.Config{Store: notifications.Persistence{DB: s}, Coordinator: monitored, Policy: policy, Clock: clock{}, Sender: transport, Report: func(category string) {
		report(category)
		if category == "outbound_gate_storage_failed" || category == "membership_storage_failed" || category == string(application.SendInvalidToken) {
			fail()
		}
	}})
	if e != nil {
		transport.Close()
		coord.Shutdown()
		return ErrStartup
	}
	if e = worker.Start(work); e != nil {
		transport.Close()
		coord.Shutdown()
		if ctx.Err() != nil {
			return nil
		}
		return ErrService
	}
	sched, e := scheduler.New(scheduler.Config{Inventory: s.Enrollments, Tracking: monitored, Policy: policy, Clock: clock{}, Report: func(category string) {
		report(category)
		if category == "inventory_failed" {
			fail()
		}
	}})
	if e != nil {
		transport.Close()
		coord.Shutdown()
		return ErrStartup
	}
	var wg sync.WaitGroup
	start := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := fn(); e != nil && !errors.Is(e, context.Canceled) && work.Err() == nil {
				fail()
			}
		}()
	}
	start(func() error {
		transport.Start(work)
		if work.Err() == nil {
			return ErrService
		}
		return nil
	})
	start(func() error { return worker.Run(work) })
	start(func() error { return sched.Run(work) })
	<-work.Done()
	coord.Shutdown()
	wg.Wait()
	transport.Close()
	select {
	case <-fatal:
		return ErrService
	default:
		return nil
	}
}
