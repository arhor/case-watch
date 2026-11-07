package config

import (
	"strings"
	"testing"
)

func validEnv() map[string]string {
	return map[string]string{"CASE_WATCH_TELEGRAM_BOT_TOKEN": "123456789:" + strings.Repeat("a", 35), "CASE_WATCH_DB_PATH": "/data/case-watch.db"}
}
func lookup(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

func TestLoadShouldAcceptRequiredSettingsWhenAccessPolicyIsAbsent(t *testing.T) {
	// Given
	env := validEnv()
	// When
	c, err := Load(lookup(env))
	// Then
	if err != nil || c.DBPath != env["CASE_WATCH_DB_PATH"] || c.TelegramBotToken != env["CASE_WATCH_TELEGRAM_BOT_TOKEN"] {
		t.Fatalf("unexpected parsed configuration: error=%v", err)
	}
}

func TestLoadShouldRejectInvalidConfigurationWhenValuesMalformed(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"CASE_WATCH_TELEGRAM_BOT_TOKEN", "secret-token"}, {"CASE_WATCH_TELEGRAM_BOT_TOKEN", "123:" + strings.Repeat("a", 34)},
		{"CASE_WATCH_DB_PATH", " "}, {"CASE_WATCH_DB_PATH", "secret\x00path"},
	} {
		t.Run(tc.key+"/"+strings.ReplaceAll(tc.value, "\x00", "NUL"), func(t *testing.T) {
			// Given
			env := validEnv()
			env[tc.key] = tc.value
			// When
			_, err := Load(lookup(env))
			// Then
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatal("expected sanitized variable-specific error")
			}
			if len(tc.value) > 4 && strings.Contains(err.Error(), tc.value) {
				t.Fatal("configuration error exposed supplied value")
			}
		})
	}
}
