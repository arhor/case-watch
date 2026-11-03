// Package telegram implements the private command transport selected by ADR 0003.
// Library types remain here. Incoming commands are not durably acknowledged.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/auth"
	"github.com/arhor/case-watch/internal/data"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type EnrollmentReader interface {
	FindByOwner(context.Context, int64) (data.Enrollment, error)
}

// Membership is ordinary authenticated evidence for the delivery worker. A private
// interaction is weaker evidence than an explicit block/unblock transition.
type Membership struct {
	Owner                           int64
	Blocked, Unblocked, Interaction bool
}
type Config struct {
	Token      string
	Endpoint   string
	HTTPClient *http.Client
	Policy     *auth.Policy
	Tracking   application.Tracking
	Store      EnrollmentReader
	Clock      application.Clock
	Diagnostic func(string)
	Membership func(context.Context, Membership)
	DirectSend func(context.Context, int64, string) (application.SendOutcome, error)
}
type state struct {
	mu                sync.Mutex
	number            string
	deadline, removal time.Time
	cancel            context.CancelFunc
}
type Adapter struct {
	bot    *bot.Bot
	config Config
	mu     sync.Mutex
	owners map[int64]*state
	wg     sync.WaitGroup
}

func New(c Config) (*Adapter, error) {
	if c.Policy == nil || c.Tracking == nil || c.Store == nil || c.Clock == nil {
		return nil, errors.New("invalid Telegram configuration")
	}
	a := &Adapter{config: c, owners: make(map[int64]*state)}
	opts := []bot.Option{bot.WithSkipGetMe(), bot.WithNotAsyncHandlers(), bot.WithAllowedUpdates(bot.AllowedUpdates{"message", "my_chat_member"}), bot.WithErrorsHandler(func(err error) {
		if !a.tokenRejected(err) {
			a.diagnostic("telegram polling failure")
		}
	}), bot.WithDefaultHandler(a.handle)}
	if c.Endpoint != "" {
		opts = append(opts, bot.WithServerURL(c.Endpoint))
	}
	if c.HTTPClient != nil {
		opts = append(opts, bot.WithHTTPClient(30*time.Second, c.HTTPClient))
	}
	b, err := bot.New(c.Token, opts...)
	if err != nil {
		return nil, errors.New("invalid Telegram configuration")
	}
	a.bot = b
	return a, nil
}

// Validate checks Telegram acceptance after local startup validation, before workers.
// ADR: docs/adr/0012_fail-fast-on-invalid-telegram-token.md.
func (a *Adapter) Validate(ctx context.Context) error {
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := a.bot.GetMe(bounded); err != nil {
		if !a.tokenRejected(err) {
			a.diagnostic("telegram validation failure")
		}
		return errors.New("telegram validation failed")
	}
	return nil
}

func (a *Adapter) tokenRejected(err error) bool {
	if errors.Is(err, bot.ErrorUnauthorized) {
		a.diagnostic("telegram_invalid_token")
		return true
	}
	return false
}

func (a *Adapter) diagnostic(s string) {
	if a.config.Diagnostic != nil {
		a.config.Diagnostic(s)
	}
}

// Start must only be called after lifecycle validation of keys and database.
// Cancellation drains library handlers, cancels enrollment work and clears drafts.
func (a *Adapter) Start(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.Sweep()
			}
		}
	}()
	a.bot.Start(ctx)
	<-done
	a.Close()
}

// Sweep drops expired plaintext drafts even when no further update arrives.
func (a *Adapter) Sweep() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.config.Clock.Now()
	for _, s := range a.owners {
		s.mu.Lock()
		if !s.deadline.IsZero() && !now.Before(s.deadline) {
			s.number = ""
			s.deadline = time.Time{}
		}
		if !s.removal.IsZero() && !now.Before(s.removal) {
			s.removal = time.Time{}
		}
		s.mu.Unlock()
	}
}
func (a *Adapter) Close() {
	a.mu.Lock()
	for _, s := range a.owners {
		s.mu.Lock()
		if s.cancel != nil {
			s.cancel()
		}
		s.number = ""
		s.deadline = time.Time{}
		s.removal = time.Time{}
		s.mu.Unlock()
	}
	a.mu.Unlock()
	a.wg.Wait()
}
func (a *Adapter) owner(id int64) *state {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.owners[id]
	if s == nil {
		s = &state{}
		a.owners[id] = s
	}
	return s
}
func (a *Adapter) reply(ctx context.Context, id int64, text string) {
	if a.config.DirectSend != nil {
		if _, err := a.config.DirectSend(ctx, id, text); err != nil {
			a.diagnostic("telegram reply failure")
		}
		return
	}
	out, err := a.Send(ctx, id, text)
	if out.Category == application.SendInvalidToken {
		a.diagnostic("telegram_invalid_token")
	}
	if err != nil {
		a.diagnostic("telegram reply failure")
	}
}
func (a *Adapter) erase(ctx context.Context, id int64, message int) {
	_, err := a.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: id, MessageID: message})
	if err != nil && !a.tokenRejected(err) {
		a.diagnostic("telegram credential deletion failure")
	}
}
func (a *Adapter) exists(ctx context.Context, id int64) (bool, error) {
	_, err := a.config.Store.FindByOwner(ctx, id)
	if errors.Is(err, data.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

const help = "Track one case privately. Commands: /enroll /status /remove /cancel /help. Automatic checks run Monday–Friday at 08:00, 10:00, 12:00, 14:00, 16:00 and 18:00 Europe/Warsaw, including holidays. Telegram enrollment is not end-to-end encrypted; platform copies may remain. Credential-message deletion is best effort."

func (a *Adapter) handle(ctx context.Context, _ *bot.Bot, u *models.Update) {
	if m := u.MyChatMember; m != nil {
		owner, err := a.config.Policy.Authenticate(auth.Identity{SenderID: m.From.ID, ChatID: m.Chat.ID, ChatType: string(m.Chat.Type), SenderIsBot: m.From.IsBot})
		if err != nil {
			return
		}
		blocked := m.NewChatMember.Type == models.ChatMemberTypeBanned
		unblocked := m.OldChatMember.Type == models.ChatMemberTypeBanned && m.NewChatMember.Type == models.ChatMemberTypeMember
		if a.config.Membership != nil && (blocked || unblocked) {
			a.config.Membership(ctx, Membership{Owner: owner.ID(), Blocked: blocked, Unblocked: unblocked})
		}
		return
	}
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	owner, err := a.config.Policy.Authenticate(auth.Identity{SenderID: m.From.ID, ChatID: m.Chat.ID, ChatType: string(m.Chat.Type), SenderIsBot: m.From.IsBot})
	if err != nil {
		return
	}
	id := owner.ID()
	if a.config.Membership != nil {
		a.config.Membership(ctx, Membership{Owner: id, Interaction: true})
	}
	s := a.owner(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := a.config.Clock.Now()
	if !s.deadline.IsZero() && !now.Before(s.deadline) {
		s.number = ""
		s.deadline = time.Time{}
	}
	if !s.removal.IsZero() && !now.Before(s.removal) {
		s.removal = time.Time{}
	}
	text := m.Text
	fields := strings.Fields(text)
	command := ""
	if len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		command = fields[0]
	}
	if command != "" && len(fields) > 1 {
		s.number = ""
		s.deadline = time.Time{}
		a.erase(ctx, id, m.ID)
		a.reply(ctx, id, "Commands take no arguments. Send /enroll to restart enrollment.")
		return
	}
	switch command {
	case "/cancel":
		if s.cancel != nil {
			s.cancel()
		}
		s.number = ""
		s.deadline = time.Time{}
		s.removal = time.Time{}
		a.reply(ctx, id, "Conversation cancelled.")
		return
	case "/start", "/help":
		a.reply(ctx, id, help)
		return
	case "/enroll":
		if s.cancel != nil {
			s.cancel()
			a.reply(ctx, id, "Enrollment cancellation requested. Send /enroll again after it finishes.")
			return
		}
		s.number = ""
		s.deadline = time.Time{}
		s.removal = time.Time{}
		exists, e := a.exists(ctx, id)
		if e != nil {
			a.reply(ctx, id, "Unable to read tracking data. Try again later.")
			return
		}
		if exists {
			a.reply(ctx, id, "You already track a case. Use /remove before enrolling another.")
			return
		}
		s.deadline = now.Add(10 * time.Minute)
		a.reply(ctx, id, "Send the case number in a separate text message. Telegram is not end-to-end encrypted; copies may remain. Deletion is best effort. Use /cancel to stop.")
		return
	case "/status":
		s.mu.Unlock()
		status, e := a.config.Tracking.Status(ctx, id)
		s.mu.Lock()
		if errors.Is(e, data.ErrNotFound) {
			a.reply(ctx, id, "No tracked case. Use /enroll.")
			return
		}
		a.reply(ctx, id, formatStatus(status, e))
		return
	case "/remove":
		if s.cancel != nil {
			s.cancel()
		}
		s.number = ""
		s.deadline = time.Time{}
		exists, e := a.exists(ctx, id)
		if e != nil {
			a.reply(ctx, id, "Unable to read tracking data.")
			return
		}
		if !exists {
			a.reply(ctx, id, "No tracked case. Use /enroll.")
			return
		}
		s.removal = now.Add(10 * time.Minute)
		a.reply(ctx, id, "Send the exact separate text REMOVE within 10 minutes to delete your credentials, history and undelivered notifications. They cannot be reconstructed. A Telegram send already accepted cannot be recalled. Use /cancel to stop.")
		return
	case "":
	default:
		if !s.deadline.IsZero() {
			a.erase(ctx, id, m.ID)
		}
		a.reply(ctx, id, "Unknown command. Use /help.")
		return
	}
	if !s.removal.IsZero() {
		if text != "REMOVE" {
			a.reply(ctx, id, "Send REMOVE exactly to confirm, or /cancel.")
			return
		}
		s.removal = time.Time{}
		s.mu.Unlock()
		e := a.config.Tracking.Remove(ctx, id)
		s.mu.Lock()
		if e != nil {
			a.reply(ctx, id, "Removal failed. Use /remove to try again.")
		} else {
			a.reply(ctx, id, "Tracking data removed. A Telegram send already accepted cannot be recalled.")
		}
		return
	}
	if s.deadline.IsZero() {
		a.reply(ctx, id, "Use /enroll to begin, or /help.")
		return
	}
	a.erase(ctx, id, m.ID)
	if s.number == "" {
		if strings.TrimSpace(text) == "" || len(text) > 256 || strings.ContainsAny(text, "\r\n") {
			a.reply(ctx, id, "Invalid case number. Send a nonempty single-line number separately.")
			return
		}
		s.number = text
		s.deadline = now.Add(10 * time.Minute)
		a.reply(ctx, id, "Send the password in a separate text message.")
		return
	}
	if text == "" || len(text) > 4096 {
		a.reply(ctx, id, "Invalid password. Send a nonempty password separately.")
		return
	}
	credentials := application.Credentials{Number: s.number, Password: text}
	s.number = ""
	s.deadline = time.Time{}
	work, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	// The intake handler remains ordered while collection can be cancelled by
	// a later command. The coordinator owns bounded work and commit fencing.
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		_, e := a.config.Tracking.Enroll(work, id, credentials)
		credentials = application.Credentials{}
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cancel = nil
		if e != nil {
			a.reply(ctx, id, "Enrollment failed. Send /enroll to restart.")
			return
		}
		a.reply(ctx, id, "Tracking enrolled. Use /status to view your case.")
	}()
}
func formatStatus(s application.Status, err error) string {
	text := ""
	if err != nil {
		text = "Refresh failed. "
	}
	if s.Observation == nil {
		text += "No successful observation available."
	} else {
		kind := "Cached"
		if s.Fresh && err == nil {
			kind = "Fresh"
		}
		text += fmt.Sprintf("%s stage: %s\nLast successful check: %s", kind, s.Observation.Stage, s.Observation.CheckedAt.UTC().Format(time.RFC3339))
	}
	if s.InProgress {
		text += "\nA check is in progress."
	}
	if !s.NextEligibleAt.IsZero() {
		text += "\nNext check eligible: " + s.NextEligibleAt.UTC().Format(time.RFC3339)
	}
	return text
}

// Send makes one API call. No direct reply or durable-send retries occur here.
func (a *Adapter) Send(ctx context.Context, id int64, text string) (application.SendOutcome, error) {
	m, err := a.bot.SendMessage(ctx, &bot.SendMessageParams{ChatID: id, Text: text})
	if err == nil {
		return application.SendOutcome{Category: application.SendAccepted, MessageID: int64(m.ID)}, nil
	}
	out := application.SendOutcome{Category: application.SendTransient}
	var rate *bot.TooManyRequestsError
	switch {
	case errors.As(err, &rate):
		out.Category = application.SendRateLimited
		out.RetryAfter = time.Duration(rate.RetryAfter) * time.Second
	case errors.Is(err, bot.ErrorUnauthorized):
		out.Category = application.SendInvalidToken
	case errors.Is(err, bot.ErrorForbidden):
		out.Category = application.SendPermanent
		if strings.Contains(strings.ToLower(err.Error()), "bot was blocked by the user") {
			out.Category = application.SendBlocked
		}
	case errors.Is(err, bot.ErrorBadRequest), errors.Is(err, bot.ErrorNotFound):
		out.Category = application.SendPermanent
	}
	return out, errors.New("telegram send failed")
}
