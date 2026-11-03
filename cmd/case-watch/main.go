// Command case-watch runs the service or explicit stopped-service maintenance.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/arhor/case-watch/internal/config"
	"github.com/arhor/case-watch/internal/credentials"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/notifications"
	"github.com/arhor/case-watch/internal/service"
)

const usage = "usage: case-watch run | provision-key PATH KEY_ID | rotate-key DB_PATH KEY_PATH NEW_ID | queue DB_PATH KEY_PATH RECIPIENT_ID | resume-recipient DB_PATH KEY_PATH RECIPIENT_ID | resume-token DB_PATH KEY_PATH"

func run(args []string, lookup func(string) (string, bool)) error {
	return execute(context.Background(), args, lookup, os.Stdout)
}
func execute(ctx context.Context, args []string, lookup func(string) (string, bool), out io.Writer) error {
	if len(args) == 3 && args[0] == "provision-key" {
		return credentials.Provision(args[1], args[2])
	}
	if len(args) == 1 && args[0] == "run" {
		// Syntax validation occurs before any runtime operation.
		if _, e := config.Load(lookup); e != nil {
			return e
		}
		return service.Run(ctx, lookup, service.Factories{})
	}
	if len(args) == 4 && args[0] == "rotate-key" {
		return service.Maintain(ctx, args[1], args[2], func(s *data.Store, k *credentials.Keyring, path string) error {
			return service.Rotate(ctx, s, k, path, args[3], nil)
		})
	}
	if len(args) == 3 && args[0] == "resume-token" {
		return service.Maintain(ctx, args[1], args[2], func(s *data.Store, _ *credentials.Keyring, _ string) error { return service.Resume(ctx, s, 0, true) })
	}
	if len(args) == 4 && (args[0] == "queue" || args[0] == "resume-recipient") {
		id, e := strconv.ParseInt(args[3], 10, 64)
		if e != nil || id <= 0 {
			return errors.New("invalid recipient ID")
		}
		return service.Maintain(ctx, args[1], args[2], func(s *data.Store, _ *credentials.Keyring, _ string) error {
			if args[0] == "resume-recipient" {
				return service.Resume(ctx, s, id, false)
			}
			rows, e := s.Pending.FindByRecipient(ctx, id)
			if e != nil {
				return e
			}
			r, g, e := (notifications.Persistence{DB: s}).DeliveryGates(ctx, id)
			if e != nil {
				return e
			}
			pending := 0
			for _, d := range rows {
				if d.Status != "delivered" {
					pending++
				}
			}
			if _, e = fmt.Fprintf(out, "recipient=%d blocked=%t pause=%s token_paused=%t rate_until=%s pending=%d records=%d\n", id, r.Blocked, safeCategory(r.Pause), g.TokenPaused, g.RateUntil.UTC().Format("2006-01-02T15:04:05Z"), pending, len(rows)); e != nil {
				return service.ErrService
			}
			for _, d := range rows {
				if _, e = fmt.Fprintf(out, "sequence=%d cycle_attempts=%d lifetime_attempts=%d status=%s failure=%s message_id=%d next_attempt=%s\n", d.Sequence, d.CycleAttempts, d.LifetimeAttempts, safeCategory(d.Status), safeCategory(d.Failure), d.MessageID, d.NextAttemptAt.UTC().Format("2006-01-02T15:04:05Z")); e != nil {
					return service.ErrService
				}
			}
			return nil
		})
	}
	return errors.New(usage)
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := execute(ctx, os.Args[1:], os.LookupEnv, os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

// Queue fields are a closed diagnostic vocabulary, even for a corrupted DB.
func safeCategory(s string) string {
	switch s {
	case "", "blocked", "permanent", "exhausted", "pending", "reserved", "retry", "delivered", "uncertain", "transient", "rate_limited", "invalid_token", "accepted":
		return s
	default:
		return "unknown"
	}
}
