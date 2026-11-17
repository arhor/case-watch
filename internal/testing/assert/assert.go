package assert

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func Equal[T comparable](t testing.TB, expected, actual T, reason ...any) {
	t.Helper()

	if expected != actual {
		t.Fatalf("expected %#v, got %#v%s", expected, actual, formatReason(reason))
	}
}

func DeepEqual[T any](t testing.TB, expected, actual T, reason ...any) {
	t.Helper()

	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("expected %#v, got %#v%s", expected, actual, formatReason(reason))
	}
}

func True(t testing.TB, value bool, reason ...any) {
	t.Helper()

	if !value {
		t.Fatalf("expected true, got false%s", formatReason(reason))
	}
}

func NoError(t testing.TB, err error, reason ...any) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v%s", err, formatReason(reason))
	}
}

func ErrorIs(t testing.TB, err, target error, reason ...any) {
	t.Helper()

	if !errors.Is(err, target) {
		t.Fatalf("expected error %v, got %v%s", target, err, formatReason(reason))
	}
}

// -- Internals ----------------------------------------------------------------

// formatReason treats a leading string as a format only when arguments follow.
// Other reasons are rendered with fmt.Sprint.
func formatReason(reason []any) string {
	length := len(reason)

	if length == 0 {
		return ""
	}
	if length == 1 {
		return " - " + fmt.Sprint(reason[0])
	}
	if format, ok := reason[0].(string); ok {
		return " - " + fmt.Sprintf(format, reason[1:]...)
	}
	return " - " + fmt.Sprint(reason...)
}
