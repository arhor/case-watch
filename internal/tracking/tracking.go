// Package tracking coordinates the owner-facing use cases in ADRs 0004–0007
// and the explicit removal amendment to ADR 0009. Network I/O never spans a
// storage transaction. One Coordinator must be shared by every runtime caller.
package tracking

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/data"
)

var (
	ErrClosed      = errors.New("tracking coordinator stopped")
	ErrDenied      = errors.New("access denied")
	ErrBusy        = errors.New("owner work in progress")
	ErrRemoving    = errors.New("removal in progress")
	ErrCheck       = errors.New("portal check failed")
	ErrCredentials = errors.New("credential operation failed")
	ErrStorage     = errors.New("tracking storage operation failed")
	ErrInvalid     = errors.New("invalid enrollment input")
)

// Store contains the atomic tracking workflows implemented by Persistence.
type Store interface {
	Enroll(context.Context, int64, application.SealedCredentials, application.Observation) error
	Observe(context.Context, int64, string, application.Observation) error
	Enrollment(context.Context, int64) (data.Enrollment, error)
	ReserveCheck(context.Context, int64, string, bool, time.Time) (data.CheckReservation, error)
	Remove(context.Context, int64, string) error
	CheckEligibility(context.Context, int64, string, bool) (time.Time, error)
}
type Vault interface {
	Seal(string, application.Credentials) (application.SealedCredentials, error)
	Open(application.SealedCredentials) (application.Credentials, error)
}

type flight struct {
	cancel     context.CancelFunc
	done       chan struct{}
	status     application.Status
	err        error
	waiters    int
	enrollment bool
}
type activity struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type ownerState struct {
	flight      *flight
	removing    bool
	removalDone chan struct{}
	activities  map[*activity]struct{}
	serial      chan struct{}
}
type Coordinator struct {
	mu     sync.Mutex
	closed bool
	owners map[int64]*ownerState
	store  Store
	vault  Vault
	portal application.Portal
	policy application.Policy
	clock  application.Clock
}

var _ application.Tracking = (*Coordinator)(nil)

func New(store Store, vault Vault, portal application.Portal, policy application.Policy, clock application.Clock) *Coordinator {
	return &Coordinator{owners: make(map[int64]*ownerState), store: store, vault: vault, portal: portal, policy: policy, clock: clock}
}
func (c *Coordinator) allowed(owner int64) bool { return owner > 0 && c.policy.Allows(owner) }
func (c *Coordinator) state(owner int64) *ownerState {
	s := c.owners[owner]
	if s == nil {
		s = &ownerState{activities: make(map[*activity]struct{}), serial: make(chan struct{}, 1)}
		c.owners[owner] = s
	}
	return s
}
func safeStore(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, data.ErrNotFound) {
		return data.ErrNotFound
	}
	if errors.Is(err, data.ErrExists) {
		return data.ErrExists
	}
	return ErrStorage
}
func canceled(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}
func (c *Coordinator) baseline(ctx context.Context, credentials application.Credentials) (application.Observation, error) {
	defer func() { credentials = application.Credentials{} }()
	bounded, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	o, err := c.portal.Check(bounded, credentials)
	if err != nil {
		return application.Observation{}, canceled(bounded, ErrCheck)
	}
	if err = bounded.Err(); err != nil {
		return application.Observation{}, err
	}
	// Completion time belongs to this coordinator, not a supplied portal timestamp.
	o.CheckedAt = c.clock.Now()
	return o, nil
}

func (c *Coordinator) Enroll(ctx context.Context, owner int64, credentials application.Credentials) (application.Observation, error) {
	defer func() { credentials = application.Credentials{} }()
	if !c.allowed(owner) {
		return application.Observation{}, ErrDenied
	}
	if ctx.Err() != nil {
		return application.Observation{}, ctx.Err()
	}
	if strings.TrimSpace(credentials.Number) == "" || strings.TrimSpace(credentials.Password) == "" {
		return application.Observation{}, ErrInvalid
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return application.Observation{}, ErrClosed
	}
	s := c.state(owner)
	if s.removing {
		c.mu.Unlock()
		return application.Observation{}, ErrRemoving
	}
	if s.flight != nil {
		c.mu.Unlock()
		return application.Observation{}, ErrBusy
	}
	_, err := c.store.Enrollment(ctx, owner)
	if err == nil {
		c.mu.Unlock()
		return application.Observation{}, data.ErrExists
	}
	if !errors.Is(err, data.ErrNotFound) {
		c.mu.Unlock()
		return application.Observation{}, safeStore(err)
	}
	work, cancel := context.WithCancel(ctx)
	f := &flight{cancel: cancel, done: make(chan struct{}), enrollment: true}
	s.flight = f
	c.mu.Unlock()
	defer func() { cancel(); c.mu.Lock(); s.flight = nil; close(f.done); c.mu.Unlock() }()
	id, err := data.NewEnrollmentID()
	if err != nil {
		return application.Observation{}, ErrStorage
	}
	o, err := c.baseline(work, credentials)
	if err != nil {
		return application.Observation{}, err
	}
	sealed, err := c.vault.Seal(id, credentials)
	credentials = application.Credentials{}
	if err != nil {
		return application.Observation{}, ErrCredentials
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.removing {
		return application.Observation{}, ErrRemoving
	}
	if work.Err() != nil {
		return application.Observation{}, work.Err()
	}
	if !c.allowed(owner) {
		return application.Observation{}, ErrDenied
	}
	if err = c.store.Enroll(work, owner, sealed, o); err != nil {
		return application.Observation{}, safeStore(err)
	}
	return o, nil
}

func cached(o application.Observation) application.Status {
	return application.Status{Observation: &o}
}
func (c *Coordinator) Status(ctx context.Context, owner int64) (application.Status, error) {
	return c.check(ctx, owner, true, false)
}

// Scheduled checks share in-flight results and the persisted case-start guard.
// A guarded case is skipped for this slot; no queued retry is created. It never
// consumes the user's manual guard. Recent successful cache is reused too.
// The scheduler must authorize its inventory too.
func (c *Coordinator) Scheduled(ctx context.Context, owner int64) (application.Status, error) {
	return c.check(ctx, owner, false, true)
}
func (c *Coordinator) check(ctx context.Context, owner int64, manual, drain bool) (application.Status, error) {
	if !c.allowed(owner) {
		return application.Status{}, ErrDenied
	}
	if ctx.Err() != nil {
		return application.Status{}, ctx.Err()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return application.Status{}, ErrClosed
	}
	s := c.state(owner)
	if s.removing {
		c.mu.Unlock()
		return application.Status{}, ErrRemoving
	}
	if f := s.flight; f != nil {
		if f.enrollment {
			c.mu.Unlock()
			return application.Status{}, ErrBusy
		}
		f.waiters++
		c.mu.Unlock()
		return c.waitCheck(ctx, s, f, drain)
	}
	e, err := c.store.Enrollment(ctx, owner)
	if err != nil {
		c.mu.Unlock()
		return application.Status{}, safeStore(err)
	}
	if e.OwnerID != owner {
		c.mu.Unlock()
		return application.Status{}, ErrDenied
	}
	now := c.clock.Now()
	status := cached(e.Observation)
	if now.Before(e.Observation.CheckedAt.Add(time.Minute)) {
		status.NextEligibleAt, err = c.store.CheckEligibility(ctx, owner, e.Credentials.ID, manual)
		status.NextEligibleAt = later(status.NextEligibleAt, e.Observation.CheckedAt.Add(time.Minute))
		c.mu.Unlock()
		return status, safeStore(err)
	}
	reservation, err := c.store.ReserveCheck(ctx, owner, e.Credentials.ID, manual, now)
	if err != nil {
		c.mu.Unlock()
		return status, safeStore(err)
	}
	status.NextEligibleAt = reservation.NextEligibleAt
	if !reservation.Reserved {
		c.mu.Unlock()
		return status, nil
	}
	work, cancel := context.WithCancel(context.WithoutCancel(ctx))
	f := &flight{cancel: cancel, done: make(chan struct{}), waiters: 1, status: status}
	s.flight = f
	c.mu.Unlock()
	go c.refresh(work, owner, s, f, e)
	return c.waitCheck(ctx, s, f, drain)
}

// Scheduled callers drain precisely the flight they joined, never a replacement.
func (c *Coordinator) waitCheck(ctx context.Context, s *ownerState, f *flight, drain bool) (application.Status, error) {
	status, err := c.wait(ctx, s, f)
	if drain && ctx.Err() != nil {
		<-f.done
	}
	return status, err
}
func (c *Coordinator) wait(ctx context.Context, s *ownerState, f *flight) (application.Status, error) {
	select {
	case <-f.done:
		return copyStatus(f.status), f.err
	case <-ctx.Done():
		c.mu.Lock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
		}
		status := copyStatus(f.status)
		status.InProgress = true
		c.mu.Unlock()
		return status, ctx.Err()
	}
}
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func copyStatus(s application.Status) application.Status {
	if s.Observation != nil {
		o := *s.Observation
		s.Observation = &o
	}
	return s
}
func (c *Coordinator) refresh(ctx context.Context, owner int64, s *ownerState, f *flight, e data.Enrollment) {
	var credentials application.Credentials
	var err error
	if !c.allowed(owner) {
		err = ErrDenied
	} else {
		credentials, err = c.vault.Open(e.Credentials)
		if err != nil {
			err = ErrCredentials
		}
	}
	var o application.Observation
	if err == nil {
		if !c.allowed(owner) {
			err = ErrDenied
		} else {
			o, err = c.baseline(ctx, credentials)
		}
	}
	credentials = application.Credentials{}
	c.mu.Lock()
	defer c.mu.Unlock()
	defer f.cancel()
	if s.removing {
		err = ErrRemoving
	} else if ctx.Err() != nil {
		err = ctx.Err()
	} else if !c.allowed(owner) {
		err = ErrDenied
	} else if err == nil {
		err = safeStore(c.store.Observe(ctx, owner, e.Credentials.ID, o))
		if err == nil {
			next := f.status.NextEligibleAt
			f.status = cached(o)
			f.status.Fresh = true
			f.status.NextEligibleAt = later(next, o.CheckedAt.Add(time.Minute))
		}
	}
	f.err = err
	s.flight = nil
	close(f.done)
}

// RunOwnerWork is the shared removal gate for notification sends and membership
// changes. Pass the immutable enrollment ID for sends; an empty ID permits an
// owner-level membership event even without an enrollment. All callbacks for an
// owner serialize, including queued events, while checks may run independently.
// The callback must honor cancellation, fence durable acknowledgements with its
// enrollment identity, return sanitized errors, and return only after all its work
// has drained. It must
// not call back into RunOwnerWork or Remove. Never put SQL/Telegram types here.
func (c *Coordinator) RunOwnerWork(ctx context.Context, owner int64, id string, fn func(context.Context) error) error {
	if !c.allowed(owner) {
		return ErrDenied
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	s := c.state(owner)
	if s.removing {
		c.mu.Unlock()
		return ErrRemoving
	}
	if id != "" {
		e, err := c.store.Enrollment(ctx, owner)
		if err != nil {
			c.mu.Unlock()
			return safeStore(err)
		}
		if e.OwnerID != owner || e.Credentials.ID != id {
			c.mu.Unlock()
			return data.ErrNotFound
		}
	}
	work, cancel := context.WithCancel(ctx)
	a := &activity{cancel: cancel, done: make(chan struct{})}
	s.activities[a] = struct{}{}
	c.mu.Unlock()
	defer func() { cancel(); c.mu.Lock(); delete(s.activities, a); close(a.done); c.mu.Unlock() }()
	select {
	case s.serial <- struct{}{}:
		defer func() { <-s.serial }()
	case <-work.Done():
		return work.Err()
	}
	if work.Err() != nil {
		return work.Err()
	}
	if !c.allowed(owner) {
		return ErrDenied
	}
	return fn(work)
}

// Remove requires confirmation at the conversation boundary. Once admitted it
// drains canceled work before returning, even when the caller cancels; cancellation
// then prevents deletion. Failed removal retains the enrollment and reopens work.
func (c *Coordinator) Remove(ctx context.Context, owner int64) error {
	if !c.allowed(owner) {
		return ErrDenied
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	s := c.state(owner)
	if s.removing {
		c.mu.Unlock()
		return ErrRemoving
	}
	s.removing = true
	s.removalDone = make(chan struct{})
	defer func() { c.mu.Lock(); s.removing = false; close(s.removalDone); s.removalDone = nil; c.mu.Unlock() }()
	var done []<-chan struct{}
	if s.flight != nil {
		s.flight.cancel()
		done = append(done, s.flight.done)
	}
	for a := range s.activities {
		a.cancel()
		done = append(done, a.done)
	}
	c.mu.Unlock()
	for _, ch := range done {
		<-ch
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !c.allowed(owner) {
		return ErrDenied
	}
	e, err := c.store.Enrollment(ctx, owner)
	if err != nil {
		return safeStore(err)
	}
	if e.OwnerID != owner {
		return ErrDenied
	}
	return safeStore(c.store.Remove(ctx, owner, e.Credentials.ID))
}

// Shutdown permanently closes admission, cancels every owner activity and drains
// work before storage may close. Concurrent calls are safe and all wait for the
// same admitted work. Collaborators must honor cancellation.
func (c *Coordinator) Shutdown() {
	c.mu.Lock()
	c.closed = true
	var done []<-chan struct{}
	for _, s := range c.owners {
		if s.flight != nil {
			s.flight.cancel()
			done = append(done, s.flight.done)
		}
		for a := range s.activities {
			a.cancel()
			done = append(done, a.done)
		}
		if s.removalDone != nil {
			done = append(done, s.removalDone)
		}
	}
	c.mu.Unlock()
	for _, ch := range done {
		<-ch
	}
}
