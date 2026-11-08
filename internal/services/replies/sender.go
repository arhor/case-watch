// Package replies sends command replies with an in-memory Telegram cooldown.
// See docs/adr/0018_remove-automatic-notifications.md.
package replies

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/domain"
)

var ErrSender = errors.New("reply operation failed")

type Config struct {
	Clock   domain.Clock
	Sender  domain.Sender
	Timeout time.Duration
	Report  func(string)
}
type Sender struct {
	c         Config
	send      sync.Mutex
	rateUntil time.Time
	failed    bool
}

func New(c Config) (*Sender, error) {
	if c.Clock == nil || c.Sender == nil {
		return nil, ErrSender
	}
	if c.Timeout <= 0 {
		c.Timeout = 15 * time.Second
	}
	return &Sender{c: c}, nil
}
func (s *Sender) report(category string) {
	if s.c.Report != nil {
		s.c.Report(category)
	}
}

// recordRateLimit requires the send lock. Recording needs no context or storage,
// so a known rate limit survives caller cancellation.
func (s *Sender) recordRateLimit(out domain.SendOutcome) {
	if out.Category != domain.SendRateLimited {
		return
	}
	delay := out.RetryAfter
	if delay <= 0 {
		delay = 30 * time.Second
	}
	until := s.c.Clock.Now().Add(delay)
	if until.After(s.rateUntil) {
		s.rateUntil = until
	}
}

// Send makes one bounded attempt for a user interaction. Replies are never queued.
func (s *Sender) Send(ctx context.Context, owner int64, text string) (domain.SendOutcome, error) {
	s.send.Lock()
	defer s.send.Unlock()
	if s.failed {
		return domain.SendOutcome{}, ErrSender
	}
	if ctx.Err() != nil {
		return domain.SendOutcome{}, ctx.Err()
	}
	if s.c.Clock.Now().Before(s.rateUntil) || owner <= 0 {
		return domain.SendOutcome{}, nil
	}
	c, cancel := context.WithTimeout(ctx, s.c.Timeout)
	out, sendErr := s.c.Sender.Send(c, owner, text)
	cancel()
	if out.Category == domain.SendInvalidToken {
		s.failed = true
		s.report(string(out.Category))
		return out, ErrSender
	}
	s.recordRateLimit(out)
	return out, sendErr
}
