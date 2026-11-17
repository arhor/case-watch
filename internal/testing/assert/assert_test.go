package assert

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestAssertionsShouldReportReasonWhenExpectationsFail(t *testing.T) {
	// Given
	sentinel := errors.New("expected")
	cases := []struct {
		name    string
		check   func(testing.TB, ...any)
		message string
	}{
		{"equal", func(tb testing.TB, reason ...any) { Equal(tb, "Approved", "Pending", reason...) }, "expected \"Approved\", got \"Pending\""},
		{"deepEqual", func(tb testing.TB, reason ...any) { DeepEqual(tb, []int{1}, []int{2}, reason...) }, "expected []int{1}, got []int{2}"},
		{"true", func(tb testing.TB, reason ...any) { True(tb, false, reason...) }, "expected true, got false"},
		{"noError", func(tb testing.TB, reason ...any) { NoError(tb, sentinel, reason...) }, "unexpected error: expected"},
		{"errorIs", func(tb testing.TB, reason ...any) { ErrorIs(tb, errors.New("actual"), sentinel, reason...) }, "expected error expected, got actual"},
	}
	reasons := []struct {
		name   string
		values []any
		suffix string
	}{
		{"omitted", nil, ""},
		{"integer", []any{42}, " - 42"},
		{"nil", []any{nil}, " - <nil>"},
		{"nonStringFirst", []any{42, " calls"}, " - 42 calls"},
		{"mixedArguments", []any{"owner %d should finish within %s: %v", int64(101), 2 * time.Second, sentinel}, " - owner 101 should finish within 2s: expected"},
		{"single", []any{"status should match the latest portal response"}, " - status should match the latest portal response"},
		{"formatted", []any{"status should match %s", "the portal response"}, " - status should match the portal response"},
		{"multipleArguments", []any{"%s should match %q", "status", "Approved"}, " - status should match \"Approved\""},
		{"formattedPercent", []any{"100%% complete for %s", "case"}, " - 100% complete for case"},
		{"literalPercent", []any{"100% complete"}, " - 100% complete"},
	}
	// When
	for _, tc := range cases {
		for _, reason := range reasons {
			t.Run(tc.name+"/"+reason.name, func(t *testing.T) {
				// Given
				recorder := &fatalRecorder{}
				// When
				stopped := recordFailure(func() { tc.check(recorder, reason.values...) })
				// Then
				if !stopped {
					t.Fatal("failed assertion should stop execution")
				}
				if want := tc.message + reason.suffix; recorder.message != want {
					t.Fatalf("expected %q, got %q", want, recorder.message)
				}
			})
		}
	}
	// Then
	// Each subtest verifies the fatal diagnostic and termination.
}

func TestAssertionsShouldContinueWhenExpectationsHold(t *testing.T) {
	// Given
	sentinel := errors.New("expected")
	wrapped := fmt.Errorf("wrapped: %w", sentinel)
	// When
	Equal(t, "Approved", "Approved", "matching stage")
	DeepEqual(t, []int{1}, []int{1}, "matching values")
	True(t, true, "condition holds")
	NoError(t, nil, "operation succeeded")
	ErrorIs(t, wrapped, sentinel, "wrapped error matches")
	// Then
	// Reaching this point confirms no assertion stopped the test.
}

type fatalRecorder struct {
	testing.TB
	message string
}

func (this *fatalRecorder) Helper() {}

func (this *fatalRecorder) Fatalf(format string, args ...any) {
	this.message = fmt.Sprintf(format, args...)
	panic(assertionStopped{})
}

type assertionStopped struct{}

func recordFailure(check func()) (stopped bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(assertionStopped); !ok {
				panic(recovered)
			}
			stopped = true
		}
	}()
	check()
	return false
}
