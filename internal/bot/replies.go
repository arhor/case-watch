package bot

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/domain"
)

var errReply = errors.New("reply operation failed")

type replySender struct {
	client     domain.Sender
	clock      domain.Clock
	diagnostic func(string)
	send       sync.Mutex
	rateUntil  time.Time
	failed     bool
}

// recordRateLimit requires the send lock. Recording needs no context or storage,
// so a known rate limit survives caller cancellation.
func (s *replySender) recordRateLimit(out domain.SendOutcome) {
	if out.Category != domain.SendRateLimited {
		return
	}
	delay := out.RetryAfter
	if delay <= 0 {
		delay = 30 * time.Second
	}
	until := s.clock.Now().Add(delay)
	if until.After(s.rateUntil) {
		s.rateUntil = until
	}
}

// Send makes one bounded attempt for a user interaction. Replies are never queued.
func (s *replySender) Send(ctx context.Context, owner int64, text string) (domain.SendOutcome, error) {
	s.send.Lock()
	defer s.send.Unlock()
	if s.failed {
		return domain.SendOutcome{}, errReply
	}
	if ctx.Err() != nil {
		return domain.SendOutcome{}, ctx.Err()
	}
	if s.clock.Now().Before(s.rateUntil) || owner <= 0 {
		return domain.SendOutcome{}, nil
	}
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	out, sendErr := s.client.Send(c, owner, text)
	cancel()
	if out.Category == domain.SendInvalidToken {
		s.failed = true
		if s.diagnostic != nil {
			s.diagnostic("telegram_invalid_token")
		}
		return out, errReply
	}
	s.recordRateLimit(out)
	return out, sendErr
}
