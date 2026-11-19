package application

import (
	"context"
	"testing"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestRun(test *testing.T) {
	test.Run("should panic with the configuration error when required token is missing", func(test *testing.T) {
		// Given
		test.Setenv("CASE_WATCH_TELEGRAM_BOT_TOKEN", "")
		test.Setenv("CASE_WATCH_DB_PATH", "unused.db")
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			_ = run(context.Background())
		}()

		// Then
		recoveredError, isError := recovered.(error)
		assert.True(test, isError, "startup should panic with an error")
		assert.Equal(test, "CASE_WATCH_TELEGRAM_BOT_TOKEN: invalid or missing token syntax", recoveredError.Error())
	})
}
