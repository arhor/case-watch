package bot

import (
	"context"
	"errors"
	"testing"
	"time"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type sendFunc func(context.Context, int64, string) (SendOutcome, error)

func (f sendFunc) Send(c context.Context, o int64, text string) (SendOutcome, error) {
	return f(c, o, text)
}

func TestSenderShouldHonorCooldownAcrossRecipientsWhenTelegramRateLimitsReply(t *testing.T) {
	for _, delay := range []time.Duration{0, time.Minute} {
		t.Run(delay.String(), func(t *testing.T) {
			// Given
			c := &clock{now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			raw := sendFunc(func(context.Context, int64, string) (SendOutcome, error) {
				calls++
				if calls == 1 {
					cancel()
					return SendOutcome{Category: SendRateLimited, RetryAfter: delay}, nil
				}
				return SendOutcome{Category: SendAccepted}, nil
			})
			s := &replySender{clock: c, client: raw}
			actual := delay
			if actual <= 0 {
				actual = 30 * time.Second
			}
			// When
			_, first := s.Send(ctx, 101, "help")
			c.now = c.now.Add(actual - time.Nanosecond)
			_, suppressed := s.Send(context.Background(), 202, "help")
			before := calls
			c.now = c.now.Add(time.Nanosecond)
			out, last := s.Send(context.Background(), 202, "help")
			// Then
			if first != nil || suppressed != nil || last != nil || before != 1 || calls != 2 || out.Category != SendAccepted {
				t.Fatalf("cooldown: calls=%d before=%d outcome=%+v errors=%v/%v/%v", calls, before, out, first, suppressed, last)
			}
		})
	}
}

func TestSenderShouldCloseAdmissionWhenTelegramRejectsToken(t *testing.T) {
	// Given
	c := &clock{now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	calls, reports := 0, 0
	s := &replySender{clock: c, client: sendFunc(func(context.Context, int64, string) (SendOutcome, error) {
		calls++
		return SendOutcome{Category: SendInvalidToken}, nil
	}), diagnostic: func(category string) {
		if category == "telegram_invalid_token" {
			reports++
		}
	}}
	// When
	_, first := s.Send(context.Background(), 101, "help")
	_, second := s.Send(context.Background(), 202, "help")
	// Then
	if !errors.Is(first, errReply) || !errors.Is(second, errReply) || calls != 1 || reports != 1 {
		t.Fatalf("token failure: %v %v calls=%d reports=%d", first, second, calls, reports)
	}
}
