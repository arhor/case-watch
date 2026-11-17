package assert

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestEqual(test *testing.T) {
	test.Run("should continue when values match", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}

		// When
		failed := recordFailure(func() {
			Equal(recorder, "Approved", "Approved", "matching stage")
		})

		// Then
		if failed {
			test.Fatalf("successful assertion failed with message %q", recorder.message)
		}
	})

	test.Run("should report failure when values differ", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}
		expectedMessage := "expected \"Approved\", got \"Pending\" - latest portal response"

		// When
		failed := recordFailure(func() {
			Equal(recorder, "Approved", "Pending", "latest portal response")
		})

		// Then
		verifyRecordedFailure(test, recorder, failed, expectedMessage)
	})

	test.Run("should continue when time instants match", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}
		expected := time.Date(2026, time.July, 6, 6, 0, 0, 0, time.UTC)
		actual := time.Date(2026, time.July, 5, 23, 0, 0, 0, time.FixedZone("", -7*60*60))

		// When
		failed := recordFailure(func() {
			Equal(recorder, expected, actual)
		})

		// Then
		if failed {
			test.Fatalf("successful assertion failed with message %q", recorder.message)
		}
	})

	test.Run("should report failure when time instants differ", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}
		expected := time.Date(2026, time.July, 6, 6, 0, 0, 0, time.UTC)
		actual := time.Date(2026, time.July, 6, 7, 0, 0, 0, time.UTC)
		expectedMessage := "expected time.Date(2026, time.July, 6, 6, 0, 0, 0, time.UTC), got time.Date(2026, time.July, 6, 7, 0, 0, 0, time.UTC)"

		// When
		failed := recordFailure(func() {
			Equal(recorder, expected, actual)
		})

		// Then
		verifyRecordedFailure(test, recorder, failed, expectedMessage)
	})

	test.Run("should report failure when an equal method differs", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}
		expectedMessage := `expected "Case", got "other"`

		// When
		failed := recordFailure(func() {
			Equal(recorder, "Case", "other")
		})

		// Then
		verifyRecordedFailure(test, recorder, failed, expectedMessage)
	})

	for _, scenario := range []struct {
		name           string
		arguments      []any
		expectedSuffix string
	}{
		{
			name:           "omitted reason",
			expectedSuffix: "",
		},
		{
			name:           "integer reason",
			arguments:      []any{42},
			expectedSuffix: " - 42",
		},
		{
			name:           "nil reason",
			arguments:      []any{nil},
			expectedSuffix: " - <nil>",
		},
		{
			name:           "non-string leading argument",
			arguments:      []any{42, " calls"},
			expectedSuffix: " - 42 calls",
		},
		{
			name:           "mixed argument types",
			arguments:      []any{"owner %d should finish within %s: %v", int64(101), 2 * time.Second, errors.New("expected")},
			expectedSuffix: " - owner 101 should finish within 2s: expected",
		},
		{
			name:           "plain text reason",
			arguments:      []any{"status should match the latest portal response"},
			expectedSuffix: " - status should match the latest portal response",
		},
		{
			name:           "single format argument",
			arguments:      []any{"status should match %s", "the portal response"},
			expectedSuffix: " - status should match the portal response",
		},
		{
			name:           "multiple format arguments",
			arguments:      []any{"%s should match %q", "status", "Approved"},
			expectedSuffix: " - status should match \"Approved\"",
		},
		{
			name:           "escaped percent in format string",
			arguments:      []any{"100%% complete for %s", "case"},
			expectedSuffix: " - 100% complete for case",
		},
		{
			name:           "literal percent without format arguments",
			arguments:      []any{"100% complete"},
			expectedSuffix: " - 100% complete",
		},
	} {
		test.Run("should report failure when values differ with "+scenario.name, func(test *testing.T) {
			// Given
			recorder := &failureRecorder{}
			expectedMessage := "expected \"Approved\", got \"Pending\"" + scenario.expectedSuffix

			// When
			failed := recordFailure(func() {
				Equal(recorder, "Approved", "Pending", scenario.arguments...)
			})

			// Then
			verifyRecordedFailure(test, recorder, failed, expectedMessage)
		})
	}
}

func TestDeepEqual(test *testing.T) {
	test.Run("should continue when contents match", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}

		// When
		failed := recordFailure(func() {
			DeepEqual(recorder, []int{1}, []int{1}, "matching values")
		})

		// Then
		if failed {
			test.Fatalf("successful assertion failed with message %q", recorder.message)
		}
	})

	test.Run("should report failure when contents differ", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}
		expectedMessage := "expected []int{1}, got []int{2} - latest values"

		// When
		failed := recordFailure(func() {
			DeepEqual(recorder, []int{1}, []int{2}, "latest values")
		})

		// Then
		verifyRecordedFailure(test, recorder, failed, expectedMessage)
	})
}

func TestTrue(test *testing.T) {
	test.Run("should continue when condition holds", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}

		// When
		failed := recordFailure(func() {
			True(recorder, true, "condition holds")
		})

		// Then
		if failed {
			test.Fatalf("successful assertion failed with message %q", recorder.message)
		}
	})

	test.Run("should report failure when condition does not hold", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}
		expectedMessage := "expected true, got false - required condition"

		// When
		failed := recordFailure(func() {
			True(recorder, false, "required condition")
		})

		// Then
		verifyRecordedFailure(test, recorder, failed, expectedMessage)
	})
}

func TestNoError(test *testing.T) {
	test.Run("should continue when error is nil", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}

		// When
		failed := recordFailure(func() {
			NoError(recorder, nil, "operation succeeded")
		})

		// Then
		if failed {
			test.Fatalf("successful assertion failed with message %q", recorder.message)
		}
	})

	test.Run("should report failure when error is present", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}
		expectedMessage := "unexpected error: connection lost - load case"

		// When
		failed := recordFailure(func() {
			NoError(recorder, errors.New("connection lost"), "load case")
		})

		// Then
		verifyRecordedFailure(test, recorder, failed, expectedMessage)
	})
}

func TestErrorIs(test *testing.T) {
	test.Run("should continue when wrapped error matches target", func(test *testing.T) {
		// Given
		targetError := errors.New("expected")
		wrappedError := fmt.Errorf("wrapped: %w", targetError)
		recorder := &failureRecorder{}

		// When
		failed := recordFailure(func() {
			ErrorIs(recorder, wrappedError, targetError, "wrapped error matches")
		})

		// Then
		if failed {
			test.Fatalf("successful assertion failed with message %q", recorder.message)
		}
	})

	test.Run("should report failure when error does not match target", func(test *testing.T) {
		// Given
		recorder := &failureRecorder{}
		expectedMessage := "expected error expected, got actual - load case"

		// When
		failed := recordFailure(func() {
			ErrorIs(recorder, errors.New("actual"), errors.New("expected"), "load case")
		})

		// Then
		verifyRecordedFailure(test, recorder, failed, expectedMessage)
	})
}

// -- Internals ----------------------------------------------------------------

func verifyRecordedFailure(test *testing.T, recorder *failureRecorder, failed bool, expectedMessage string) {
	test.Helper()

	if !failed {
		test.Fatal("failed assertion should stop execution")
	}
	if recorder.message != expectedMessage {
		test.Fatalf("expected diagnostic %q, got %q", expectedMessage, recorder.message)
	}
}

type failureRecorder struct {
	testing.TB
	message string
}

type assertionFailed struct{}

func (this *failureRecorder) Helper() {}

func (this *failureRecorder) Fatalf(format string, arguments ...any) {
	this.message = fmt.Sprintf(format, arguments...)
	panic(assertionFailed{})
}

func recordFailure(assertion func()) (failed bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, isAssertionStop := recovered.(assertionFailed); !isAssertionStop {
				panic(recovered)
			}
			failed = true
		}
	}()
	assertion()
	return false
}
