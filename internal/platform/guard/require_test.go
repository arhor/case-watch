package guard_test

import (
	"errors"
	"testing"

	"github.com/arhor/case-watch/internal/platform/guard"
	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestRequire(test *testing.T) {
	test.Run("should return a nonempty string unchanged", func(test *testing.T) {
		// Given
		token := "secret"

		// When
		result := guard.Require("Token", token)

		// Then
		assert.Equal(test, token, result)
	})

	test.Run("should return the original nonnil pointer", func(test *testing.T) {
		// Given
		value := 42
		pointer := &value

		// When
		result := guard.Require("Count", pointer)

		// Then
		assert.Equal(test, pointer, result)
	})

	test.Run("should panic with the property name for an empty string", func(test *testing.T) {
		// Given
		expectedMessage := "required property is missing: Token"
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			guard.Require("Token", "")
		}()

		// Then
		assert.Equal[any](test, expectedMessage, recovered)
	})

	test.Run("should panic with the property name for a nil pointer", func(test *testing.T) {
		// Given
		expectedMessage := "required property is missing: Store"
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			guard.Require("Store", (*int)(nil))
		}()

		// Then
		assert.Equal[any](test, expectedMessage, recovered)
	})

	test.Run("should panic with the property name for a zero number", func(test *testing.T) {
		// Given
		expectedMessage := "required property is missing: Count"
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			guard.Require("Count", 0)
		}()

		// Then
		assert.Equal[any](test, expectedMessage, recovered)
	})

	test.Run("should panic with the property name for a false boolean", func(test *testing.T) {
		// Given
		expectedMessage := "required property is missing: Enabled"
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			guard.Require("Enabled", false)
		}()

		// Then
		assert.Equal[any](test, expectedMessage, recovered)
	})
}

func TestMust(test *testing.T) {
	test.Run("should return the original value when there is no error", func(test *testing.T) {
		// Given
		value := []string{"a", "b"}

		// When
		result := guard.Must(value, nil)

		// Then
		assert.True(test, &result[0] == &value[0], "should preserve the original slice")
	})

	test.Run("should panic with the original error", func(test *testing.T) {
		// Given
		expectedError := errors.New("failed to initialize")
		var recovered any

		// When
		func() {
			defer func() { recovered = recover() }()
			guard.Must(0, expectedError)
		}()

		// Then
		assert.Equal[any](test, expectedError, recovered)
	})
}
