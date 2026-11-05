package config

import (
	"strings"
	"testing"
)

func validEnv() map[string]string {
	return map[string]string{"CASE_WATCH_TELEGRAM_BOT_TOKEN": "123456789:" + strings.Repeat("a", 35), "CASE_WATCH_DB_PATH": "/data/case-watch.db", "CASE_WATCH_ALLOWED_USER_IDS": "123, 456,123"}
}
func lookup(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

func TestLoadShouldDefaultToRestrictedAndDeduplicateWhenBooleanMissing(t *testing.T) {
	// Given
	env := validEnv()
	// When
	c, err := Load(lookup(env))
	// Then
	if err != nil || c.AllowAllUsers || len(c.AllowedUserIDs) != 2 || c.AllowedUserIDs[1] != 456 {
		t.Fatalf("unexpected parsed configuration: error=%v", err)
	}
}

func TestLoadShouldRejectInvalidConfigurationWhenValuesMalformed(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"CASE_WATCH_TELEGRAM_BOT_TOKEN", "secret-token"}, {"CASE_WATCH_TELEGRAM_BOT_TOKEN", "123:" + strings.Repeat("a", 34)},
		{"CASE_WATCH_DB_PATH", " "}, {"CASE_WATCH_DB_PATH", "secret\x00path"},
		{"CASE_WATCH_ALLOW_ALL_USERS", "TRUE"}, {"CASE_WATCH_ALLOW_ALL_USERS", "1"}, {"CASE_WATCH_ALLOW_ALL_USERS", ""},
		{"CASE_WATCH_ALLOWED_USER_IDS", ""}, {"CASE_WATCH_ALLOWED_USER_IDS", "0"}, {"CASE_WATCH_ALLOWED_USER_IDS", "-1"}, {"CASE_WATCH_ALLOWED_USER_IDS", "+1"},
		{"CASE_WATCH_ALLOWED_USER_IDS", "9223372036854775808"}, {"CASE_WATCH_ALLOWED_USER_IDS", "123,,456"}, {"CASE_WATCH_ALLOWED_USER_IDS", "123,"}, {"CASE_WATCH_ALLOWED_USER_IDS", "secret-id"},
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

func TestLoadShouldValidateAllowlistWhenAllowAllEnabled(t *testing.T) {
	for _, raw := range []string{"", "  ", "123,123", "0", "-1", "+1", "9223372036854775808", "123,", ",123", "123,,456", "secret"} {
		t.Run(raw, func(t *testing.T) {
			// Given
			env := validEnv()
			env["CASE_WATCH_ALLOW_ALL_USERS"] = " true "
			env["CASE_WATCH_ALLOWED_USER_IDS"] = raw
			wantValid := strings.TrimSpace(raw) == "" || raw == "123,123"
			// When
			c, err := Load(lookup(env))
			// Then
			if (err == nil) != wantValid || (err == nil && !c.AllowAllUsers) {
				t.Fatalf("unexpected validation outcome: %v", err)
			}
		})
	}
}
