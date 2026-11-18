package guard_test

import (
	"errors"
	"testing"

	"github.com/arhor/case-watch/internal/platform/guard"
)

func TestRequireReturnsValue(t *testing.T) {
	if got := guard.Require("Token", "secret"); got != "secret" {
		t.Fatalf("expected secret, got %q", got)
	}

	value := 42
	if got := guard.Require("Count", &value); got != &value {
		t.Fatalf("expected original pointer, got %p", got)
	}
}

func TestRequirePanicsOnZeroValue(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check func()
		want  any
	}{
		{"empty string", func() { guard.Require("Token", "") }, "required property is missing: Token"},
		{"nil pointer", func() { guard.Require("Store", (*int)(nil)) }, "required property is missing: Store"},
		{"zero number", func() { guard.Require("Count", 0) }, "required property is missing: Count"},
		{"false boolean", func() { guard.Require("Enabled", false) }, "required property is missing: Enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.want {
					t.Fatalf("expected panic %v, got %v", tc.want, got)
				}
			}()
			tc.check()
		})
	}
}

func TestMustReturnsValue(t *testing.T) {
	value := []string{"a", "b"}
	if got := guard.Must(value, nil); &got[0] != &value[0] {
		t.Fatal("expected original slice")
	}
}

func TestMustPanicsWithOriginalError(t *testing.T) {
	want := errors.New("failed to initialize")
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("expected original error %v, got %v", want, got)
		}
	}()
	guard.Must(0, want)
}
