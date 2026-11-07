package application

import (
	"context"
	"sync"

	"github.com/arhor/case-watch/internal/repositories"
	"github.com/arhor/case-watch/internal/services/tracking"
)

type runner interface {
	Run(context.Context) error
}

type telegramBot interface {
	runner
	Close()
}

type delivery interface {
	runner
	Start(context.Context) error
}

// runtime owns acquired resources and the lifetime of constructed components.
// It is private and is never passed to application services.
type runtime struct {
	work        context.Context
	cancel      context.CancelFunc
	fatal       chan struct{}
	failOnce    sync.Once
	store       *repositories.Store
	lock        *databaseLock
	tracking    *tracking.Coordinator
	client      telegramClient
	telegramBot telegramBot
	worker      delivery
	scheduler   runner
}

func newRuntime(ctx context.Context) *runtime {
	work, cancel := context.WithCancel(ctx)
	return &runtime{work: work, cancel: cancel, fatal: make(chan struct{})}
}

func (r *runtime) fail() {
	r.failOnce.Do(func() { close(r.fatal); r.cancel() })
}

func (r *runtime) result() error {
	select {
	case <-r.fatal:
		return errService
	default:
		return nil
	}
}

func (r *runtime) run() error {
	var wg sync.WaitGroup
	start := func(component runner) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = component.Run(r.work)
			// All three components run until canceled. Any earlier return,
			// including nil or context.Canceled, is an unexpected termination.
			if r.work.Err() == nil {
				r.fail()
			}
		}()
	}
	start(r.telegramBot)
	start(r.worker)
	start(r.scheduler)
	<-r.work.Done()
	// Detached tracking flights need explicit cancellation before joins.
	r.tracking.Shutdown()
	wg.Wait()
	return r.result()
}

// close is also safe for a partially constructed runtime. Runtime loops must
// have joined before closing storage. Keep the advisory lock through DB close.
func (r *runtime) close() error {
	r.cancel()
	if r.tracking != nil {
		r.tracking.Shutdown()
	}
	if r.telegramBot != nil {
		r.telegramBot.Close()
	}
	var result error
	if r.store != nil {
		if err := r.store.Close(); err != nil {
			result = errService
		}
	}
	if r.lock != nil {
		if err := r.lock.close(); err != nil {
			result = errService
		}
	}
	return result
}
