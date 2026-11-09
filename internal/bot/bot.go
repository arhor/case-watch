// Package bot implements the private command transport selected by ADR 0003.
// Library types remain here. Incoming commands are not durably acknowledged.
package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/services/cases"
	"github.com/arhor/case-watch/internal/utils"
	"github.com/go-telegram/bot/models"
)

type Cases interface {
	Enroll(ctx context.Context, owner int64, credentials domain.Credentials) (domain.Observation, error)
	Status(ctx context.Context, owner int64) (domain.Status, error)
	Remove(ctx context.Context, owner int64) error
	HasEnrollment(ctx context.Context, owner int64) (bool, error)
}

type Transport interface {
	domain.Sender
	Poll(ctx context.Context, handler func(context.Context, *models.Update)) error
	DeleteMessage(ctx context.Context, owner int64, message int) error
}

type Config struct {
	Client     Transport
	Cases      Cases
	Clock      utils.Clock
	Diagnostic func(string)
}
type state struct {
	mu                sync.Mutex
	number            string
	deadline, removal time.Time
	cancel            context.CancelFunc
}
type Bot struct {
	client  Transport
	replies *replySender
	config  Config
	mu      sync.Mutex
	owners  map[int64]*state
	wg      sync.WaitGroup
}

func New(c Config) (*Bot, error) {
	if c.Client == nil || c.Cases == nil || c.Clock == nil {
		return nil, errors.New("invalid Telegram configuration")
	}
	sender := &replySender{client: c.Client, clock: c.Clock, diagnostic: c.Diagnostic}
	return &Bot{client: c.Client, replies: sender, config: c, owners: make(map[int64]*state)}, nil
}

func (a *Bot) diagnostic(s string) {
	if a.config.Diagnostic != nil {
		a.config.Diagnostic(s)
	}
}

// Run must only be called after token validation.
// Cancellation drains library handlers, cancels enrollment work and clears drafts.
func (a *Bot) Run(ctx context.Context) error {
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
	err := a.client.Poll(ctx, a.handle)
	cancel()
	<-done
	a.Close()
	return err
}

func withLock(mu *sync.Mutex, fn func()) {
	mu.Lock()
	defer mu.Unlock()
	fn()
}

// Sweep drops expired plaintext drafts even when no further update arrives.
func (this *Bot) Sweep() {
	this.mu.Lock()
	defer this.mu.Unlock()

	now := this.config.Clock.Now()
	for _, state := range this.owners {
		withLock(&state.mu, func() {
			if !state.deadline.IsZero() && !now.Before(state.deadline) {
				state.number = ""
				state.deadline = time.Time{}
			}
			if !state.removal.IsZero() && !now.Before(state.removal) {
				state.removal = time.Time{}
			}
		})
	}
}

func (this *Bot) Close() {
	withLock(&this.mu, func() {
		for _, state := range this.owners {
			withLock(&state.mu, func() {
				if state.cancel != nil {
					state.cancel()
				}
				state.number = ""
				state.deadline = time.Time{}
				state.removal = time.Time{}
			})
		}
	})
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

func (this *Bot) reply(ctx context.Context, id int64, text string) {
	if _, err := this.replies.Send(ctx, id, text); err != nil {
		this.diagnostic("telegram reply failure")
	}
}

func (a *Bot) erase(ctx context.Context, id int64, message int) {
	err := a.client.DeleteMessage(ctx, id, message)
	if err != nil && !errors.Is(err, ErrInvalidToken) {
		a.diagnostic("telegram credential deletion failure")
	}
}

func (a *Bot) exists(ctx context.Context, id int64) (bool, error) {
	return a.config.Cases.HasEnrollment(ctx, id)
}

const help = "Track one case privately. Commands: /enroll /status /remove /cancel /help. " +
	"Automatic checks run Monday-Friday at 08:00, 10:00, 12:00, 14:00, 16:00 and 18:00 Europe/Warsaw, including holidays. " +
	"Checks do not send automatic notifications; use /status to view your case. Telegram enrollment is not end-to-end encrypted; " +
	"platform copies may remain. Credential-message deletion is best effort."

// privateOwner binds conversations to Telegram transport metadata.
func privateOwner(sender *models.User, chat models.Chat) (int64, bool) {
	if sender == nil || sender.IsBot || sender.ID <= 0 || chat.Type != models.ChatTypePrivate || chat.ID != sender.ID {
		return 0, false
	}
	return sender.ID, true
}

func (a *Bot) handle(ctx context.Context, u *models.Update) {
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
			a.reply(ctx, id, "Unable to read tracking repositories. Try again later.")
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
		status, e := a.config.Cases.Status(ctx, id)
		s.mu.Lock()
		if errors.Is(e, cases.ErrNotFound) {
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
			a.reply(ctx, id, "Unable to read tracking repositories.")
			return
		}
		if !exists {
			a.reply(ctx, id, "No tracked case. Use /enroll.")
			return
		}
		s.removal = now.Add(10 * time.Minute)
		a.reply(ctx, id, "Send the exact separate text REMOVE within 10 minutes to delete your credentials and history. They cannot be reconstructed. Use /cancel to stop.")
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
