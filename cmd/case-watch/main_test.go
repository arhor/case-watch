package main

import (
	"github.com/arhor/case-watch/internal/credentials"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestRunShouldRejectUnknownCommandWithoutReadingEnvironmentWhenArgsInvalid(t *testing.T) {
	// Given
	calls := 0
	// When
	err := run([]string{"provision-key"}, func(string) (string, bool) { calls++; return "", false })
	// Then
	if err == nil || calls != 0 || err.Error() != usage {
		t.Fatal("unknown command must fail without side effects")
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
