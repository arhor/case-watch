package bot

import (
	"context"
	"errors"
	"sync"
	"time"
)

var errReply = errors.New("reply operation failed")

type replySender struct {
	client     Sender
	diagnostic func(string)
	send       sync.Mutex
	failed     bool
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
	if owner <= 0 {
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
	return out, sendErr
}
