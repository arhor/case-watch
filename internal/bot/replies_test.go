package bot

import (
	"context"
	"errors"
	"testing"
)

type sendFunc func(context.Context, int64, string) (SendOutcome, error)

func (f sendFunc) Send(c context.Context, o int64, text string) (SendOutcome, error) {
	return f(c, o, text)
}

func TestSenderShouldAttemptSubsequentRepliesWhenSendFails(t *testing.T) {
	// Given
	failure := errors.New("send failed")
	var recipients []int64
	s := &replySender{client: sendFunc(func(_ context.Context, owner int64, _ string) (SendOutcome, error) {
		recipients = append(recipients, owner)
		if len(recipients) == 1 {
			return SendOutcome{Category: SendFailed}, failure
		}
		return SendOutcome{Category: SendAccepted}, nil
	})}
	// When
	_, first := s.Send(context.Background(), 101, "help")
	same, second := s.Send(context.Background(), 101, "help")
	other, third := s.Send(context.Background(), 202, "help")
	// Then
	if !errors.Is(first, failure) || second != nil || third != nil || same.Category != SendAccepted || other.Category != SendAccepted {
		t.Fatalf("outcomes=%+v/%+v errors=%v/%v/%v", same, other, first, second, third)
	}
	if len(recipients) != 3 || recipients[0] != 101 || recipients[1] != 101 || recipients[2] != 202 {
		t.Fatalf("recipients=%v", recipients)
	}
}

func TestSenderShouldCloseAdmissionWhenTelegramRejectsToken(t *testing.T) {
	// Given
	calls, reports := 0, 0
	s := &replySender{client: sendFunc(func(context.Context, int64, string) (SendOutcome, error) {
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
