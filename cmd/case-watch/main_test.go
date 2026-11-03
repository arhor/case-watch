package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arhor/case-watch/internal/credentials"
)

func TestRunShouldRejectInvalidConfigWhenServiceRequested(t *testing.T) {
	// Given
	lookup := func(string) (string, bool) { return "sensitive-input", true }

	// When
	err := run([]string{"run"}, lookup)

	// Then
	if err == nil || !strings.Contains(err.Error(), "CASE_WATCH_TELEGRAM_BOT_TOKEN") || strings.Contains(err.Error(), "sensitive-input") {
		t.Fatal("expected sanitized configuration failure")
	}
}

func TestRunShouldRejectStartupWhenRequiredPathsUnavailable(t *testing.T) {
	// Given
	env := map[string]string{"CASE_WATCH_TELEGRAM_BOT_TOKEN": "123:" + strings.Repeat("a", 35), "CASE_WATCH_DB_PATH": "/data/db", "CASE_WATCH_KEY_FILE": "/keys/keyring", "CASE_WATCH_ALLOW_ALL_USERS": "true"}
	// When
	err := run([]string{"run"}, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	// Then
	if err == nil || err.Error() != "service startup validation failed" {
		t.Fatal("startup must reject unavailable paths")
	}
}

func TestRunShouldRejectInvalidArgumentsWithoutReadingEnvironmentWhenSyntaxInvalid(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"run", "extra"},
		{"provision-key"},
		{"provision-key", "key", "initial", "extra"},
		{"rotate-key", "database", "key"},
		{"rotate-key", "database", "key", "new", "extra"},
		{"resume-token", "database"},
		{"resume-token", "database", "key", "extra"},
		{"queue", "database", "key"},
		{"queue", "database", "key", "123", "extra"},
		{"resume-recipient", "database", "key"},
		{"resume-recipient", "database", "key", "123", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// Given
			environmentCalls := 0
			lookupEnv := func(string) (string, bool) {
				environmentCalls++
				return "", false
			}

			// When
			err := run(args, lookupEnv)

			// Then
			if err == nil || err.Error() != usage || environmentCalls != 0 {
				t.Fatalf("expected usage without environment access; got error %v and %d lookups", err, environmentCalls)
			}
		})
	}
}

func TestRunShouldRejectInvalidRecipientIDWhenRecipientCommandRequested(t *testing.T) {
	for _, command := range []string{"queue", "resume-recipient"} {
		for _, recipientID := range []string{"", "abc", "0", "-1", "9223372036854775808"} {
			t.Run(command+"/"+recipientID, func(t *testing.T) {
				// Given
				args := []string{command, "database", "key", recipientID}
				lookupEnv := func(string) (string, bool) {
					t.Fatal("recipient validation must not read the environment")
					return "", false
				}

				// When
				err := run(args, lookupEnv)

				// Then
				if err == nil || err.Error() != "invalid recipient ID" {
					t.Fatalf("expected invalid recipient ID; got %v", err)
				}
			})
		}
	}
}

func TestRunShouldProvisionWithoutEnvironmentWhenOperatorRequestsKey(t *testing.T) {
	// Given
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "keyring")
	calls := 0
	// When
	err := run([]string{"provision-key", path, "initial"}, func(string) (string, bool) { calls++; return "", false })
	k, loadErr := credentials.Load(path)
	// Then
	if err != nil || loadErr != nil || k == nil || calls != 0 {
		t.Fatal("explicit provisioning failed")
	}
}

func run(args []string, lookupEnv func(string) (string, bool)) error {
	return execute(context.Background(), args, lookupEnv, os.Stdout)
}
