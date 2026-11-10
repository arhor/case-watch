package bot

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/utils"
)

var errReply = errors.New("reply operation failed")

type replySender struct {
	client     Sender
	clock      utils.Clock
	diagnostic func(string)
	send       sync.Mutex
	rateUntil  time.Time
	failed     bool
}

// recordRateLimit requires the send lock. Recording needs no context or storage,
// so a known rate limit survives caller cancellation.
func (s *replySender) recordRateLimit(out SendOutcome) {
	if out.Category != SendRateLimited {
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
func (s *replySender) Send(ctx context.Context, owner int64, text string) (SendOutcome, error) {
	s.send.Lock()
	defer s.send.Unlock()
	if s.failed {
		return SendOutcome{}, errReply
	}
	if ctx.Err() != nil {
		return SendOutcome{}, ctx.Err()
	}
	if s.clock.Now().Before(s.rateUntil) || owner <= 0 {
		return SendOutcome{}, nil
	}
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	out, sendErr := s.client.Send(c, owner, text)
	cancel()
	if out.Category == SendInvalidToken {
		s.failed = true
		if s.diagnostic != nil {
			s.diagnostic("telegram_invalid_token")
		}
		return out, errReply
	}
	s.recordRateLimit(out)
	return out, sendErr
}
