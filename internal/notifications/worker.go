// Package notifications delivers durable ordered transitions under ADR 0013.
package notifications

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/data"
)

var ErrWorker = errors.New("notification operation failed")

type Store interface {
	DeliveryHeads(context.Context) ([]data.Delivery, error)
	ReserveDelivery(context.Context, int64, time.Time) (data.Delivery, error)
	CompleteDelivery(context.Context, data.Delivery, application.SendOutcome, time.Time) error
	RecoverDeliveries(context.Context, time.Time) error
	RecipientState(context.Context, int64) (data.RecipientState, error)
	SetBlocked(context.Context, int64, bool) error
	ResumeUnblocked(context.Context, int64) error
	ReleaseDelivery(context.Context, data.Delivery) error
}
type Coordinator interface {
	RunOwnerWork(context.Context, int64, string, func(context.Context) error) error
}

// Event is authenticated private membership evidence, independent of Telegram types.
type Event struct {
	Owner                           int64
	Blocked, Unblocked, Interaction bool
}
type Config struct {
	Store       Store
	Coordinator Coordinator
	Policy      application.Policy
	Clock       application.Clock
	Sender      application.Sender
	Timeout     time.Duration
	// Report receives fixed operational categories, never transport errors or text.
	Report func(string)
}
type Worker struct {
	c       Config
	mu      sync.Mutex
	started bool
	pending map[int64][]Event
	// Serializing sends also ensures a newly reported global gate prevents the next send.
	rateUntil time.Time // guarded by send; process-local cooldown under ADR 0013
	failed    bool      // guarded by send; a rejected token closes outbound admission until restart
	send      sync.Mutex
	eventMu   sync.Mutex
}

func New(c Config) (*Worker, error) {
	if c.Store == nil || c.Coordinator == nil || c.Policy == nil || c.Clock == nil || c.Sender == nil {
		return nil, ErrWorker
	}
	if c.Timeout <= 0 {
		c.Timeout = 15 * time.Second
	}
	return &Worker{c: c, pending: map[int64][]Event{}}, nil
}
func (w *Worker) report(category string) {
	if w.c.Report != nil {
		w.c.Report(category)
	}
}
func (w *Worker) bounded(ctx context.Context, fn func(context.Context) error) error {
	c, cancel := context.WithTimeout(ctx, w.c.Timeout)
	defer cancel()
	return fn(c)
}

// Start recovers uncertain reservations once, before any Step sends. Call before
// starting incoming polling. A failed recovery leaves the worker stopped.
func (w *Worker) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return nil
	}
	if err := w.bounded(ctx, func(c context.Context) error { return w.c.Store.RecoverDeliveries(c, w.c.Clock.Now()) }); err != nil {
		return ErrWorker
	}
	w.started = true
	return nil
}

// Membership retains evidence before attempting it. Cancellation/removal cannot
// lose an unblock; a later Step retries it through the same owner gate.
func (w *Worker) Membership(ctx context.Context, e Event) {
	if e.Owner <= 0 || (!e.Blocked && !e.Unblocked && !e.Interaction) || !w.c.Policy.Allows(e.Owner) {
		return
	}
	w.mu.Lock()
	w.pending[e.Owner] = append(w.pending[e.Owner], e)
	w.mu.Unlock()
	w.events(ctx, e.Owner)
}
func (w *Worker) events(ctx context.Context, owner int64) {
	// Owner serialization alone does not prevent two callers reading the same event;
	// use a distinct gate for event dequeue and application.
	w.eventMu.Lock()
	defer w.eventMu.Unlock()
	for {
		w.mu.Lock()
		list := w.pending[owner]
		if len(list) == 0 {
			w.mu.Unlock()
			return
		}
		e := list[0]
		w.mu.Unlock()
		if !w.c.Policy.Allows(owner) {
			return
		}
		err := w.c.Coordinator.RunOwnerWork(ctx, owner, "", func(c context.Context) error {
			err := w.bounded(c, func(b context.Context) error {
				if e.Unblocked && !e.Blocked {
					return w.c.Store.ResumeUnblocked(b, owner)
				}
				return w.c.Store.SetBlocked(b, owner, e.Blocked)
			})
			if err != nil && c.Err() == nil {
				w.report("membership_storage_failed")
			}
			return err
		})
		if err != nil {
			w.report("membership_deferred")
			return
		}
		w.mu.Lock()
		w.pending[owner] = w.pending[owner][1:]
		w.mu.Unlock()
	}
}

// Step makes at most one attempt per recipient. Call repeatedly for catch-up;
// waiting and suppressed work never reserve an attempt.
func (w *Worker) Step(ctx context.Context) error {
	w.mu.Lock()
	started := w.started
	owners := make([]int64, 0, len(w.pending))
	for o := range w.pending {
		owners = append(owners, o)
	}
	w.mu.Unlock()
	if !started {
		return ErrWorker
	}
	for _, o := range owners {
		w.events(ctx, o)
	}
	var heads []data.Delivery
	if err := w.bounded(ctx, func(c context.Context) error { var e error; heads, e = w.c.Store.DeliveryHeads(c); return e }); err != nil {
		return ErrWorker
	}
	for _, head := range heads {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !w.c.Policy.Allows(head.RecipientID) {
			continue
		}
		err := w.c.Coordinator.RunOwnerWork(ctx, head.RecipientID, head.EnrollmentID, func(c context.Context) error {
			w.send.Lock()
			defer w.send.Unlock()
			if w.failed {
				return ErrWorker
			}
			if w.c.Clock.Now().Before(w.rateUntil) {
				return nil
			}
			if !w.c.Policy.Allows(head.RecipientID) {
				return nil
			}
			return w.bounded(c, func(b context.Context) error {
				d, e := w.c.Store.ReserveDelivery(b, head.RecipientID, w.c.Clock.Now())
				if errors.Is(e, data.ErrNotEligible) || errors.Is(e, data.ErrNotFound) {
					return nil
				}
				if e != nil {
					return ErrWorker
				}
				// Check again at the network boundary. A reservation whose send is
				// suppressed remains uncertain, never falsely delivered.
				r, e := w.c.Store.RecipientState(b, d.RecipientID)
				if e != nil {
					if w.release(b, d) != nil {
						return ErrWorker
					}
					return ErrWorker
				}
				if !w.c.Policy.Allows(d.RecipientID) || r.Blocked || r.Pause != "" || w.c.Clock.Now().Before(w.rateUntil) || b.Err() != nil {
					return w.release(b, d)
				}
				text := "Case stage changed:\n" + d.PreviousStage + " → " + d.Stage
				out, sendErr := w.c.Sender.Send(b, d.RecipientID, text)
				if sendErr != nil && (out.Category == "" || out.Category == application.SendAccepted) {
					out = application.SendOutcome{Category: application.SendTransient}
				}
				switch out.Category {
				case application.SendAccepted:
					if out.MessageID <= 0 {
						out = application.SendOutcome{Category: application.SendTransient}
					}
				case application.SendTransient, application.SendBlocked, application.SendRateLimited, application.SendInvalidToken, application.SendPermanent:
				default:
					out = application.SendOutcome{Category: application.SendTransient}
				}
				if out.RetryAfter < 0 {
					out.RetryAfter = 0
				}
				if out.Category == application.SendInvalidToken {
					w.failed = true
				}
				w.recordRateLimit(out)
				// Removal cancels the network call, but outcome persistence must drain
				// before its deletion transaction and must not inherit that cancellation.
				ack, cancel := context.WithTimeout(context.WithoutCancel(c), w.c.Timeout)
				defer cancel()
				if e = w.c.Store.CompleteDelivery(ack, d, out, w.c.Clock.Now()); e != nil {
					w.report("delivery_uncertain")
					return ErrWorker
				}
				if out.Category == application.SendInvalidToken {
					w.report(string(out.Category))
					return ErrWorker
				}
				if out.Category != application.SendAccepted {
					w.report(string(out.Category))
				}
				return nil
			})
		})
		if err != nil {
			w.report("delivery_deferred")
			if errors.Is(err, ErrWorker) {
				return ErrWorker
			}
		}
	}
	return nil
}

// Run owns startup recovery and uses Step for deterministic policy testing.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.Start(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := w.Step(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// recordRateLimit requires the send lock. Recording needs no context or storage,
// so a known rate limit survives caller cancellation and outcome storage failure.
func (w *Worker) recordRateLimit(out application.SendOutcome) {
	if out.Category != application.SendRateLimited {
		return
	}
	delay := out.RetryAfter
	if delay <= 0 {
		delay = 30 * time.Second
	}
	until := w.c.Clock.Now().Add(delay)
	if until.After(w.rateUntil) {
		w.rateUntil = until
	}
}

func (w *Worker) release(ctx context.Context, d data.Delivery) error {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.c.Timeout)
	defer cancel()
	if w.c.Store.ReleaseDelivery(c, d) != nil {
		return ErrWorker
	}
	return nil
}

// SendDirect serializes gate admission, a single raw transport attempt and global
// cooldown updates with durable sends. It never changes notification rows.
// Rate deadlines are retained after caller cancellation; token rejection closes admission.
func (w *Worker) SendDirect(ctx context.Context, owner int64, text string) (application.SendOutcome, error) {
	w.send.Lock()
	defer w.send.Unlock()
	if w.failed {
		return application.SendOutcome{}, ErrWorker
	}
	if ctx.Err() != nil {
		return application.SendOutcome{}, ctx.Err()
	}
	if w.c.Clock.Now().Before(w.rateUntil) || !w.c.Policy.Allows(owner) {
		return application.SendOutcome{}, nil
	}
	c, cancel := context.WithTimeout(ctx, w.c.Timeout)
	out, sendErr := w.c.Sender.Send(c, owner, text)
	cancel()
	if out.Category == application.SendInvalidToken {
		w.failed = true
		w.report(string(out.Category))
		return out, ErrWorker
	}
	w.recordRateLimit(out)
	return out, sendErr
}
