package application

import (
	"strings"
	"testing"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func validEnv() map[string]string {
	return map[string]string{"CASE_WATCH_TELEGRAM_BOT_TOKEN": "123456789:" + strings.Repeat("a", 35), "CASE_WATCH_DB_PATH": "/data/case-watch.db"}
}
func lookup(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

func TestLoadConfigShouldAcceptRequiredSettingsWhenAccessPolicyIsAbsent(t *testing.T) {
	// Given
	env := validEnv()
	// When
	c, err := loadConfig(lookup(env))
	// Then
	assert.NoError(t, err, "unexpected parsed configuration")
	assert.Equal(t, env["CASE_WATCH_DB_PATH"], c.DBPath, "unexpected parsed configuration")
	assert.Equal(t, env["CASE_WATCH_TELEGRAM_BOT_TOKEN"], c.TelegramBotToken, "unexpected parsed configuration")
}

func TestLoadConfigShouldRejectInvalidConfigurationWhenValuesMalformed(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"CASE_WATCH_TELEGRAM_BOT_TOKEN", "secret-token"}, {"CASE_WATCH_TELEGRAM_BOT_TOKEN", "123:" + strings.Repeat("a", 34)},
		{"CASE_WATCH_DB_PATH", " "}, {"CASE_WATCH_DB_PATH", "secret\x00path"},
	} {
		t.Run(tc.key+"/"+strings.ReplaceAll(tc.value, "\x00", "NUL"), func(t *testing.T) {
			// Given
			env := validEnv()
			env[tc.key] = tc.value
			// When
			_, err := loadConfig(lookup(env))
			// Then
			assert.True(t, err != nil, "expected sanitized variable-specific error")
			assert.True(t, strings.Contains(err.Error(), tc.key), "expected sanitized variable-specific error")
			assert.True(t, !(len(tc.value) > 4 && strings.Contains(err.Error(), tc.value)), "configuration error exposed supplied value")
		})
	}
}
