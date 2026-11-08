// Package tracking coordinates the owner-facing use cases in ADRs 0004–0007
// and ADR 0018 removal of notifications. Network I/O never spans a
// storage transaction. One Coordinator must be shared by every runtime caller.
package tracking

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/repositories"
)

var (
	ErrClosed   = errors.New("tracking coordinator stopped")
	ErrDenied   = errors.New("access denied")
	ErrBusy     = errors.New("owner work in progress")
	ErrRemoving = errors.New("removal in progress")
	ErrCheck    = errors.New("portal check failed")
	ErrStorage  = errors.New("tracking storage operation failed")
	ErrInvalid  = errors.New("invalid enrollment input")
	ErrNotFound = repositories.ErrNotFound
)

// Store contains the atomic tracking workflows implemented by Persistence.
type Store interface {
	Enroll(context.Context, int64, string, domain.Credentials, domain.Observation) error
	Observe(context.Context, int64, string, domain.Observation) error
	Enrollment(context.Context, int64) (repositories.Enrollment, error)
	ReserveCheck(context.Context, int64, string, bool, time.Time) (repositories.CheckReservation, error)
	Remove(context.Context, int64, string) error
	CheckEligibility(context.Context, int64, string, bool) (time.Time, error)
	Enrollments(context.Context) ([]repositories.Enrollment, error)
}

// HasEnrollment lets interactive entry points ask a business-level question
// without reading an enrollment repository directly.
func (c *Coordinator) HasEnrollment(ctx context.Context, owner int64) (bool, error) {
	if owner <= 0 {
		return false, ErrDenied
	}
	_, err := c.store.Enrollment(ctx, owner)
	if errors.Is(err, repositories.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, safeStore(err)
	}
	return true, nil
}

// ScheduledOwners returns the enrolled owners available for scheduler
// traversal. The scheduler entry point never reads repositories itself.
func (c *Coordinator) ScheduledOwners(ctx context.Context) ([]int64, error) {
	items, err := c.store.Enrollments(ctx)
	if err != nil {
		return nil, safeStore(err)
	}
	owners := make([]int64, 0, len(items))
	for _, item := range items {
		if item.OwnerID > 0 {
			owners = append(owners, item.OwnerID)
		}
	}
	return owners, nil
}

type flight struct {
	cancel     context.CancelFunc
	done       chan struct{}
	status     domain.Status
	err        error
	waiters    int
	enrollment bool
}
type ownerState struct {
	flight      *flight
	removing    bool
	removalDone chan struct{}
}
type Coordinator struct {
	mu     sync.Mutex
	closed bool
	owners map[int64]*ownerState
	store  Store
	portal domain.Portal
	clock  domain.Clock
}

var _ domain.Tracking = (*Coordinator)(nil)

func New(store Store, portal domain.Portal, clock domain.Clock) *Coordinator {
	return &Coordinator{owners: make(map[int64]*ownerState), store: store, portal: portal, clock: clock}
}
func (c *Coordinator) state(owner int64) *ownerState {
	s := c.owners[owner]
	if s == nil {
		s = &ownerState{}
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
	if errors.Is(err, repositories.ErrNotFound) {
		return repositories.ErrNotFound
	}
	if errors.Is(err, repositories.ErrExists) {
		return repositories.ErrExists
	}
	return ErrStorage
}
func canceled(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}
func (c *Coordinator) baseline(ctx context.Context, credentials domain.Credentials) (domain.Observation, error) {
	defer func() { credentials = domain.Credentials{} }()
	bounded, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	o, err := c.portal.Check(bounded, credentials)
	if err != nil {
		return domain.Observation{}, canceled(bounded, ErrCheck)
	}
	if err = bounded.Err(); err != nil {
		return domain.Observation{}, err
	}
	// Completion time belongs to this coordinator, not a supplied portal timestamp.
	o.CheckedAt = c.clock.Now()
	return o, nil
}

func (c *Coordinator) Enroll(ctx context.Context, owner int64, credentials domain.Credentials) (domain.Observation, error) {
	defer func() { credentials = domain.Credentials{} }()
	if owner <= 0 {
		return domain.Observation{}, ErrDenied
	}
	if ctx.Err() != nil {
		return domain.Observation{}, ctx.Err()
	}
	if strings.TrimSpace(credentials.Number) == "" || strings.TrimSpace(credentials.Password) == "" {
		return domain.Observation{}, ErrInvalid
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return domain.Observation{}, ErrClosed
	}
	s := c.state(owner)
	if s.removing {
		c.mu.Unlock()
		return domain.Observation{}, ErrRemoving
	}
	if s.flight != nil {
		c.mu.Unlock()
		return domain.Observation{}, ErrBusy
	}
	_, err := c.store.Enrollment(ctx, owner)
	if err == nil {
		c.mu.Unlock()
		return domain.Observation{}, repositories.ErrExists
	}
	if !errors.Is(err, repositories.ErrNotFound) {
		c.mu.Unlock()
		return domain.Observation{}, safeStore(err)
	}
	work, cancel := context.WithCancel(ctx)
	f := &flight{cancel: cancel, done: make(chan struct{}), enrollment: true}
	s.flight = f
	c.mu.Unlock()
	defer func() { cancel(); c.mu.Lock(); s.flight = nil; close(f.done); c.mu.Unlock() }()
	id, err := repositories.NewEnrollmentID()
	if err != nil {
		return domain.Observation{}, ErrStorage
	}
	o, err := c.baseline(work, credentials)
	if err != nil {
		return domain.Observation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.removing {
		return domain.Observation{}, ErrRemoving
	}
	if work.Err() != nil {
		return domain.Observation{}, work.Err()
	}
	if err = c.store.Enroll(work, owner, id, credentials, o); err != nil {
		return domain.Observation{}, safeStore(err)
	}
	return o, nil
}

func cached(o domain.Observation) domain.Status {
	return domain.Status{Observation: &o}
}
func (c *Coordinator) Status(ctx context.Context, owner int64) (domain.Status, error) {
	return c.check(ctx, owner, true, false)
}

// Scheduled checks share in-flight results and the persisted case-start guard.
// A guarded case is skipped for this slot; no queued retry is created. It never
// consumes the user's manual guard. Recent successful cache is reused too.
func (c *Coordinator) Scheduled(ctx context.Context, owner int64) (domain.Status, error) {
	return c.check(ctx, owner, false, true)
}
func (c *Coordinator) check(ctx context.Context, owner int64, manual, drain bool) (domain.Status, error) {
	if owner <= 0 {
		return domain.Status{}, ErrDenied
	}
	if ctx.Err() != nil {
		return domain.Status{}, ctx.Err()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return domain.Status{}, ErrClosed
	}
	s := c.state(owner)
	if s.removing {
		c.mu.Unlock()
		return domain.Status{}, ErrRemoving
	}
	if f := s.flight; f != nil {
		if f.enrollment {
			c.mu.Unlock()
			return domain.Status{}, ErrBusy
		}
		f.waiters++
		c.mu.Unlock()
		return c.waitCheck(ctx, s, f, drain)
	}
	e, err := c.store.Enrollment(ctx, owner)
	if err != nil {
		c.mu.Unlock()
		return domain.Status{}, safeStore(err)
	}
	if e.OwnerID != owner {
		c.mu.Unlock()
		return domain.Status{}, ErrDenied
	}
	now := c.clock.Now()
	status := cached(e.Observation)
	if now.Before(e.Observation.CheckedAt.Add(time.Minute)) {
		status.NextEligibleAt, err = c.store.CheckEligibility(ctx, owner, e.ID, manual)
		status.NextEligibleAt = later(status.NextEligibleAt, e.Observation.CheckedAt.Add(time.Minute))
		c.mu.Unlock()
		return status, safeStore(err)
	}
	reservation, err := c.store.ReserveCheck(ctx, owner, e.ID, manual, now)
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
func (c *Coordinator) waitCheck(ctx context.Context, s *ownerState, f *flight, drain bool) (domain.Status, error) {
	status, err := c.wait(ctx, s, f)
	if drain && ctx.Err() != nil {
		<-f.done
	}
	return status, err
}
func (c *Coordinator) wait(ctx context.Context, s *ownerState, f *flight) (domain.Status, error) {
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
func copyStatus(s domain.Status) domain.Status {
	if s.Observation != nil {
		o := *s.Observation
		s.Observation = &o
	}
	return s
}
func (c *Coordinator) refresh(ctx context.Context, owner int64, s *ownerState, f *flight, e repositories.Enrollment) {
	credentials := e.Credentials
	o, err := c.baseline(ctx, credentials)
	credentials = domain.Credentials{}
	c.mu.Lock()
	defer c.mu.Unlock()
	defer f.cancel()
	if s.removing {
		err = ErrRemoving
	} else if ctx.Err() != nil {
		err = ctx.Err()
	} else if err == nil {
		err = safeStore(c.store.Observe(ctx, owner, e.ID, o))
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

// Remove requires confirmation at the conversation boundary. Once admitted it
// drains canceled work before returning, even when the caller cancels; cancellation
// then prevents deletion. Failed removal retains the enrollment and reopens work.
func (c *Coordinator) Remove(ctx context.Context, owner int64) error {
	if owner <= 0 {
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
	f := s.flight
	if f != nil {
		f.cancel()
	}
	c.mu.Unlock()
	if f != nil {
		<-f.done
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	e, err := c.store.Enrollment(ctx, owner)
	if err != nil {
		return safeStore(err)
	}
	if e.OwnerID != owner {
		return ErrDenied
	}
	return safeStore(c.store.Remove(ctx, owner, e.ID))
}

// Shutdown permanently closes admission, cancels every owner check and drains
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
		if s.removalDone != nil {
			done = append(done, s.removalDone)
		}
	}
	c.mu.Unlock()
	for _, ch := range done {
		<-ch
	}
}
