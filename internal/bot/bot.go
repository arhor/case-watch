// Package bot implements the private command transport selected by ADR 0003.
// Library types remain here. Incoming commands are not durably acknowledged.
package bot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/services/cases"
	telegramapi "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

var ErrInvalidToken = errors.New("telegram token rejected")

type Cases interface {
	Enroll(context.Context, int64, domain.Credentials) (domain.Observation, error)
	Status(context.Context, int64) (domain.Status, error)
	Remove(context.Context, int64) error
	HasEnrollment(context.Context, int64) (bool, error)
}

type Config struct {
	Token      string
	Endpoint   string
	HTTPClient *http.Client
	Cases      Cases
	Clock      domain.Clock
	Diagnostic func(string)
}
type state struct {
	mu                sync.Mutex
	number            string
	deadline, removal time.Time
	cancel            context.CancelFunc
}

type commandHandler func(context.Context, *models.Message, *state, int64, time.Time)

type Bot struct {
	client     *telegramapi.Bot
	config     Config
	mu         sync.Mutex
	owners     map[int64]*state
	wg         sync.WaitGroup
	polling    atomic.Bool
	send       sync.Mutex
	rateUntil  time.Time
	sendFailed bool
	handlers   map[string]commandHandler
}

func New(c Config) (*Bot, error) {
	if c.Cases == nil || c.Clock == nil {
		return nil, errors.New("invalid Telegram configuration")
	}
	a := &Bot{config: c, owners: make(map[int64]*state)}
	a.handlers = map[string]commandHandler{
		"/cancel": a.onCancel,
		"/start":  a.onHelp,
		"/help":   a.onHelp,
		"/enroll": a.onEnroll,
		"/status": a.onStatus,
		"/remove": a.onRemove,
	}
	options := []telegramapi.Option{
		telegramapi.WithSkipGetMe(),
		telegramapi.WithNotAsyncHandlers(),
		telegramapi.WithAllowedUpdates(telegramapi.AllowedUpdates{"message"}),
		telegramapi.WithDefaultHandler(a.handle),
		telegramapi.WithErrorsHandler(func(err error) {
			if !a.tokenRejected(err) {
				a.diagnostic("telegram polling failure")
			}
		}),
	}
	if c.Endpoint != "" {
		options = append(options, telegramapi.WithServerURL(c.Endpoint))
	}
	if c.HTTPClient != nil {
		options = append(options, telegramapi.WithHTTPClient(30*time.Second, c.HTTPClient))
	}
	client, err := telegramapi.New(c.Token, options...)
	if err != nil {
		return nil, errors.New("invalid Telegram configuration")
	}
	a.client = client
	return a, nil
}

func (a *Bot) diagnostic(s string) {
	if a.config.Diagnostic != nil {
		a.config.Diagnostic(s)
	}
}

func (a *Bot) tokenRejected(err error) bool {
	if errors.Is(err, telegramapi.ErrorUnauthorized) {
		a.diagnostic("telegram_invalid_token")
		return true
	}
	return false
}

// Validate checks token acceptance before background work starts.
func (a *Bot) Validate(ctx context.Context) error {
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := a.client.GetMe(bounded); err != nil {
		if a.tokenRejected(err) {
			return ErrInvalidToken
		}
		a.diagnostic("telegram validation failure")
		return errors.New("telegram validation failed")
	}
	return nil
}

// Run must only be called after token validation.
// Cancellation drains library handlers, cancels enrollment work and clears drafts.
func (a *Bot) Run(ctx context.Context) error {
	if !a.polling.CompareAndSwap(false, true) {
		return errors.New("telegram polling already started")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
	a.client.Start(ctx)
	cancel()
	<-done
	a.Close()
	return ctx.Err()
}

// Sweep drops expired plaintext drafts even when no further update arrives.
func (a *Bot) Sweep() {
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
func (a *Bot) Close() {
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
func (a *Bot) owner(id int64) *state {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.owners[id]
	if s == nil {
		s = &state{}
		a.owners[id] = s
	}
	return s
}
func (a *Bot) reply(ctx context.Context, id int64, text string) {
	a.send.Lock()
	defer a.send.Unlock()
	if a.sendFailed || ctx.Err() != nil || a.config.Clock.Now().Before(a.rateUntil) || id <= 0 {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	_, err := a.client.SendMessage(bounded, &telegramapi.SendMessageParams{ChatID: id, Text: text})
	cancel()
	if err == nil {
		return
	}
	var rate *telegramapi.TooManyRequestsError
	switch {
	case errors.As(err, &rate):
		delay := time.Duration(rate.RetryAfter) * time.Second
		if delay <= 0 {
			delay = 30 * time.Second
		}
		until := a.config.Clock.Now().Add(delay)
		if until.After(a.rateUntil) {
			a.rateUntil = until
		}
	case a.tokenRejected(err):
		a.sendFailed = true
	default:
		a.diagnostic("telegram reply failure")
	}
}
func (a *Bot) erase(ctx context.Context, id int64, message int) {
	_, err := a.client.DeleteMessage(ctx, &telegramapi.DeleteMessageParams{ChatID: id, MessageID: message})
	if err != nil && !a.tokenRejected(err) {
		a.diagnostic("telegram credential deletion failure")
	}
}
func (a *Bot) exists(ctx context.Context, id int64) (bool, error) {
	return a.config.Cases.HasEnrollment(ctx, id)
}

const help = "Track one case privately. Commands: /enroll /status /remove /cancel /help. Automatic checks run Monday–Friday at 08:00, 10:00, 12:00, 14:00, 16:00 and 18:00 Europe/Warsaw, including holidays. Checks do not send automatic notifications; use /status to view your case. Telegram enrollment is not end-to-end encrypted; platform copies may remain. Credential-message deletion is best effort."

// privateOwner binds conversations to Telegram transport metadata.
func privateOwner(sender *models.User, chat models.Chat) (int64, bool) {
	if sender == nil || sender.IsBot || sender.ID <= 0 || chat.Type != models.ChatTypePrivate || chat.ID != sender.ID {
		return 0, false
	}
	return sender.ID, true
}

func (a *Bot) onCancel(ctx context.Context, _ *models.Message, s *state, id int64, _ time.Time) {
	if s.cancel != nil {
		s.cancel()
	}
	s.number = ""
	s.deadline = time.Time{}
	s.removal = time.Time{}
	a.reply(ctx, id, "Conversation cancelled.")
}

func (a *Bot) onHelp(ctx context.Context, _ *models.Message, _ *state, id int64, _ time.Time) {
	a.reply(ctx, id, help)
}

func (a *Bot) onEnroll(ctx context.Context, _ *models.Message, s *state, id int64, now time.Time) {
	if s.cancel != nil {
		s.cancel()
		a.reply(ctx, id, "Enrollment cancellation requested. Send /enroll again after it finishes.")
		return
	}
	s.number = ""
	s.deadline = time.Time{}
	s.removal = time.Time{}
	exists, err := a.exists(ctx, id)
	if err != nil {
		a.reply(ctx, id, "Unable to read tracking repositories. Try again later.")
		return
	}
	if exists {
		a.reply(ctx, id, "You already track a case. Use /remove before enrolling another.")
		return
	}
	s.deadline = now.Add(10 * time.Minute)
	a.reply(ctx, id, "Send the case number in a separate text message. Telegram is not end-to-end encrypted; copies may remain. Deletion is best effort. Use /cancel to stop.")
}

func (a *Bot) onStatus(ctx context.Context, _ *models.Message, s *state, id int64, _ time.Time) {
	s.mu.Unlock()
	status, err := a.config.Cases.Status(ctx, id)
	s.mu.Lock()
	if errors.Is(err, cases.ErrNotFound) {
		a.reply(ctx, id, "No tracked case. Use /enroll.")
		return
	}
	a.reply(ctx, id, formatStatus(status, err))
}

func (a *Bot) onRemove(ctx context.Context, _ *models.Message, s *state, id int64, now time.Time) {
	if s.cancel != nil {
		s.cancel()
	}
	s.number = ""
	s.deadline = time.Time{}
	exists, err := a.exists(ctx, id)
	if err != nil {
		a.reply(ctx, id, "Unable to read tracking repositories.")
		return
	}
	if !exists {
		a.reply(ctx, id, "No tracked case. Use /enroll.")
		return
	}
	s.removal = now.Add(10 * time.Minute)
	a.reply(ctx, id, "Send the exact separate text REMOVE within 10 minutes to delete your credentials and history. They cannot be reconstructed. Use /cancel to stop.")
}

func (a *Bot) handle(ctx context.Context, _ *telegramapi.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	id, ok := privateOwner(m.From, m.Chat)
	if !ok {
		return
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
	if handler, ok := a.handlers[command]; ok {
		handler(ctx, m, s, id, now)
		return
	}
	if command != "" {
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
		e := a.config.Cases.Remove(ctx, id)
		s.mu.Lock()
		if e != nil {
			a.reply(ctx, id, "Removal failed. Use /remove to try again.")
		} else {
			a.reply(ctx, id, "Tracking data removed.")
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
	credentials := domain.Credentials{Number: s.number, Password: text}
	s.number = ""
	s.deadline = time.Time{}
	work, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	// The intake handler remains ordered while collection can be cancelled by
	// a later command. The case service owns bounded work and commit fencing.
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		_, e := a.config.Cases.Enroll(work, id, credentials)
		credentials = domain.Credentials{}
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
func formatStatus(s domain.Status, err error) string {
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
