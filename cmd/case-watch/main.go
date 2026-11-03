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
	"time"

	"github.com/arhor/case-watch/internal/config"
	"github.com/arhor/case-watch/internal/credentials"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/notifications"
	"github.com/arhor/case-watch/internal/service"
)

const usage = "usage: case-watch --run | --provision-key PATH KEY_ID | --rotate-key DB_PATH KEY_PATH NEW_ID | --queue DB_PATH KEY_PATH RECIPIENT_ID"

func main() {
	ctx, stop :=
		signal.NotifyContext(
			context.Background(),
			os.Interrupt,
			syscall.SIGTERM,
		)

	defer stop()

	if err := execute(ctx, os.Args[1:], os.LookupEnv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, lookup func(string) (string, bool), output io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}

	command := args[0]
	commandArgs := args[1:]

	switch command {
	case "--run":
		return onRun(ctx, commandArgs, lookup)
	case "--provision-key":
		return onProvisionKey(commandArgs)
	case "--rotate-key":
		return onRotateKey(ctx, commandArgs)
	case "--queue":
		return onQueue(ctx, commandArgs, output)
	default:
		return errors.New(usage)
	}
}

// ------------------------------ Command Handlers ------------------------------

func onRun(ctx context.Context, args []string, lookup func(string) (string, bool)) error {
	if len(args) != 0 {
		return errors.New(usage)
	}
	if _, err := config.Load(lookup); err != nil {
		return err
	}

	return service.Run(
		ctx,
		lookup,
		service.Factories{},
	)
}

func onProvisionKey(args []string) error {
	if len(args) != 2 {
		return errors.New(usage)
	}
	keyPath, keyID := args[0], args[1]

	return credentials.Provision(
		keyPath,
		keyID,
	)
}

func onRotateKey(ctx context.Context, args []string) error {
	if len(args) != 3 {
		return errors.New(usage)
	}
	databasePath, keyPath, newKeyID := args[0], args[1], args[2]

	return service.Maintain(
		ctx,
		databasePath,
		keyPath,
		func(store *data.Store, keyring *credentials.Keyring, canonicalKeyPath string) error {
			return service.Rotate(
				ctx,
				store,
				keyring,
				canonicalKeyPath,
				newKeyID,
				nil,
			)
		},
	)
}

func onQueue(ctx context.Context, args []string, output io.Writer) error {
	if len(args) != 3 {
		return errors.New(usage)
	}
	databasePath, keyPath, recipientIDArg := args[0], args[1], args[2]
	recipientID, err := strconv.ParseInt(recipientIDArg, 10, 64)

	if err != nil || recipientID <= 0 {
		return errors.New("invalid recipient ID")
	}

	return service.Maintain(
		ctx,
		databasePath,
		keyPath,
		func(store *data.Store, _ *credentials.Keyring, _ string) error {
			return printQueue(
				ctx,
				store,
				recipientID,
				output,
			)
		},
	)
}

// ------------------------------ Internal Helpers ------------------------------

func printQueue(ctx context.Context, store *data.Store, recipientID int64, output io.Writer) error {
	deliveries, err := store.Pending.FindByRecipient(ctx, recipientID)
	if err != nil {
		return err
	}
	persistence := notifications.Persistence{DB: store}
	recipientState, err := persistence.RecipientState(ctx, recipientID)
	if err != nil {
		return err
	}

	pendingCount := 0
	for _, delivery := range deliveries {
		if delivery.Status != "delivered" {
			pendingCount++
		}
	}
	if _, err := fmt.Fprintf(
		output,
		"recipient=%d blocked=%t pause=%s pending=%d records=%d\n",
		recipientID,
		recipientState.Blocked,
		safeCategory(recipientState.Pause),
		pendingCount,
		len(deliveries),
	); err != nil {
		return service.ErrService
	}
	for _, delivery := range deliveries {
		if _, err := fmt.Fprintf(
			output,
			"sequence=%d cycle_attempts=%d lifetime_attempts=%d status=%s failure=%s message_id=%d next_attempt=%s\n",
			delivery.Sequence,
			delivery.CycleAttempts,
			delivery.LifetimeAttempts,
			safeCategory(delivery.Status),
			safeCategory(delivery.Failure),
			delivery.MessageID,
			delivery.NextAttemptAt.UTC().Format(time.RFC3339),
		); err != nil {
			return service.ErrService
		}
	}
	return nil
}

func safeCategory(category string) string {
	switch category {
	case "", "blocked", "permanent", "exhausted", "pending", "reserved", "retry", "delivered", "uncertain", "transient", "rate_limited", "invalid_token", "accepted":
		return category
	default:
		return "unknown"
	}
}
