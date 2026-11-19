package application

import (
	"strings"
	"testing"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestLoadConfig(test *testing.T) {
	test.Run("should accept required settings when access policy is absent", func(test *testing.T) {
		// Given
		environment := validEnvironment()

		// When
		config, err := loadConfig(lookup(environment))

		// Then
		assert.NoError(test, err, "unexpected parsed configuration")
		assert.Equal(test, environment["CASE_WATCH_DB_PATH"], config.DBPath, "unexpected parsed configuration")
		assert.Equal(test, environment["CASE_WATCH_TELEGRAM_BOT_TOKEN"], config.TelegramBotToken, "unexpected parsed configuration")
	})

	for _, scenario := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "should reject configuration when token is malformed", key: "CASE_WATCH_TELEGRAM_BOT_TOKEN", value: "secret-token"},
		{name: "should reject configuration when token suffix is too short", key: "CASE_WATCH_TELEGRAM_BOT_TOKEN", value: "123:" + strings.Repeat("a", 34)},
		{name: "should reject configuration when database path is blank", key: "CASE_WATCH_DB_PATH", value: " "},
		{name: "should reject configuration when database path contains NUL", key: "CASE_WATCH_DB_PATH", value: "secret\x00path"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			// Given
			environment := validEnvironment()
			environment[scenario.key] = scenario.value

			// When
			_, err := loadConfig(lookup(environment))

			// Then
			assert.True(test, err != nil, "invalid configuration should return an error")
			assert.True(test, strings.Contains(err.Error(), scenario.key), "error should identify the invalid variable")
			assert.True(test, !(len(scenario.value) > 4 && strings.Contains(err.Error(), scenario.value)), "error should not expose the supplied value")
		})
	}
}

// -- Internals ----------------------------------------------------------------

func validEnvironment() map[string]string {
	return map[string]string{"CASE_WATCH_TELEGRAM_BOT_TOKEN": "123456789:" + strings.Repeat("a", 35), "CASE_WATCH_DB_PATH": "/data/case-watch.db"}
}

func lookup(environment map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { value, ok := environment[key]; return value, ok }
}
