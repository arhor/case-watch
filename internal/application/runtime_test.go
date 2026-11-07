package application

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/arhor/case-watch/internal/auth"
	"github.com/arhor/case-watch/internal/services/tracking"
)

type loopFunc func(context.Context) error

func (f loopFunc) Run(ctx context.Context) error { return f(ctx) }

type testIntake struct{ loopFunc }

func (testIntake) Close() {}

type testDelivery struct{ loopFunc }

func (testDelivery) Start(context.Context) error { return nil }

func TestRuntimeShouldCancelAndJoinSiblingsWhenAnyComponentReturnsUnexpectedly(t *testing.T) {
	for _, source := range []string{"intake", "delivery", "scheduler"} {
		for _, outcome := range []string{"nil", "error", "canceled"} {
			t.Run(source+"/"+outcome, func(t *testing.T) {
				// Given
				r := newRuntime(context.Background())
				defer r.close()
				r.tracking = tracking.New(nil, nil, auth.New(true, nil), nil)
				var joined atomic.Int32
				loop := func(name string) loopFunc {
					return func(ctx context.Context) error {
						if name == source {
							switch outcome {
							case "error":
								return errors.New("synthetic-secret")
							case "canceled":
								return context.Canceled
							default:
								return nil
							}
						}
						<-ctx.Done()
						joined.Add(1)
						return ctx.Err()
					}
				}
				r.intake = testIntake{loop("intake")}
				r.worker = testDelivery{loop("delivery")}
				r.scheduler = loop("scheduler")
				// When
				err := r.run()
				// Then
				if !errors.Is(err, errService) || joined.Load() != 2 {
					t.Fatalf("error=%v joined siblings=%d", err, joined.Load())
				}
				if _, err := r.tracking.Status(context.Background(), 1); !errors.Is(err, tracking.ErrClosed) {
					t.Fatalf("tracking admission remains open: %v", err)
				}
			})
		}
	}
}

func TestRuntimeShouldReturnFailureWhenFatalSignalPrecedesRun(t *testing.T) {
	// Given
	r := newRuntime(context.Background())
	defer r.close()
	r.tracking = tracking.New(nil, nil, nil, nil)
	loop := loopFunc(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	r.intake = testIntake{loop}
	r.worker = testDelivery{loop}
	r.scheduler = loop
	r.fail()
	// When
	err := r.run()
	// Then
	if !errors.Is(err, errService) {
		t.Fatalf("early fatal signal was lost: %v", err)
	}
}
