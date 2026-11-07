// Package application constructs Case Watch and owns its process lifetime.
package application

import (
	"context"
	"errors"
	"os"

	"github.com/arhor/case-watch/internal/config"
	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/entrypoints/telegram"
	telegramrepository "github.com/arhor/case-watch/internal/repositories/telegram"
)

type telegramClient interface {
	telegram.Client
	domain.Sender
	Validate(context.Context) error
}

// dependencies overrides only external boundaries for same-package offline tests.
// Production construction and injection remain in wiring.go.
type dependencies struct {
	lookup   func(string) (string, bool)
	report   func(string)
	portal   func() domain.Portal
	telegram func(telegramrepository.Config) (telegramClient, error)
}

// Run loads startup configuration, constructs the application, and blocks until
// cancellation or fatal failure. All admitted work drains before storage closes.
func Run(ctx context.Context) error {
	return run(ctx, dependencies{lookup: os.LookupEnv})
}

func run(ctx context.Context, d dependencies) (result error) {
	c, err := config.Load(d.lookup)
	if err != nil {
		return err
	}
	r := newRuntime(ctx)
	defer func() {
		if err := r.close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	if err := wire(r, c, d); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	// ADR 0012: validate before recovery, polling, delivery or scheduling.
	if err := r.client.Validate(r.work); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errStartup
	}
	if r.work.Err() != nil {
		return r.result()
	}
	if err := r.worker.Start(r.work); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errService
	}
	return r.run()
}
