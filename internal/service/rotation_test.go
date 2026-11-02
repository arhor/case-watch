package service

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/credentials"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/notifications"
	"github.com/arhor/case-watch/internal/tracking"
)

func TestRotateShouldRetainDecryptabilityAndIdentityWhenInterruptedAtEachDurableBoundary(t *testing.T) {
	// Given
	for _, stage := range []string{"keys_persisted", "record", "ciphertext_committed", "verified", "complete"} {
		t.Run(stage, func(t *testing.T) {
			f := setup(t)
			s := f.open(t)
			defer s.Close()
			ctx := context.Background()
			a := seed(t, s, f.k, 1)
			seed(t, s, f.k, 2)
			if e := (tracking.Persistence{DB: s}).Observe(ctx, 1, a.ID, application.Observation{Stage: "B", CheckedAt: time.Now()}); e != nil {
				t.Fatal(e)
			}
			before, e := s.Enrollments.FindAll(ctx)
			if e != nil {
				t.Fatal(e)
			}
			history, _ := s.History.History(ctx, 1, a.ID)
			queue, _ := s.Pending.Queue(ctx, 1)
			records := 0
			// When
			e = Rotate(ctx, s, f.k, f.key, "new", func(s string) error {
				if s == "record" {
					records++
					if stage == s && records == 2 {
						return errors.New("synthetic-secret")
					}
				} else if stage == s {
					return errors.New("synthetic-secret")
				}
				return nil
			})
			persisted, load := credentials.Load(f.key)
			verify := Verify(ctx, s, persisted)
			after, read := s.Enrollments.FindAll(ctx)
			// Then
			if load != nil || verify != nil || read != nil {
				t.Fatal("interruption made records undecryptable")
			}
			if stage == "complete" {
				if e != nil {
					t.Fatal(e)
				}
			} else if !errors.Is(e, ErrService) {
				t.Fatal("fault not surfaced safely")
			}
			probe, pe := persisted.Seal(a.ID, application.Credentials{})
			if pe != nil {
				t.Fatal(pe)
			}
			expected := "old"
			if stage == "complete" {
				expected = "new"
			}
			if probe.KeyID != expected {
				t.Fatal("active key changed before verification")
			}
			for i, row := range after {
				if row.OwnerID != before[i].OwnerID || row.Credentials.ID != before[i].Credentials.ID || row.Observation != before[i].Observation {
					t.Fatal("rotation changed identity or baseline")
				}
				if stage == "keys_persisted" || stage == "record" {
					if row.Credentials.KeyID != "old" || !bytes.Equal(row.Credentials.Payload, before[i].Credentials.Payload) {
						t.Fatal("partial transaction survived")
					}
				} else if row.Credentials.KeyID != "new" {
					t.Fatal("committed ciphertext not rotated")
				}
				if _, e := persisted.Open(before[i].Credentials); e != nil {
					t.Fatal("old key was discarded")
				}
			}
			h, _ := s.History.History(ctx, 1, a.ID)
			q, _ := s.Pending.Queue(ctx, 1)
			if !reflect.DeepEqual(h, history) || !reflect.DeepEqual(q, queue) {
				t.Fatal("rotation changed durable delivery/history")
			}
			if stage != "complete" {
				if e := Rotate(ctx, s, persisted, f.key, "new", nil); e != nil {
					t.Fatal("interrupted rotation could not resume")
				}
				k, e := credentials.Load(f.key)
				if e != nil || Verify(ctx, s, k) != nil {
					t.Fatal("resumed rotation invalid")
				}
			}
		})
	}
}

func TestResumeShouldPreserveLifetimeAndHistoryWhenRecoveringStoppedReservation(t *testing.T) {
	// Given
	f := setup(t)
	s := f.open(t)
	ctx := context.Background()
	r := seed(t, s, f.k, 1)
	now := time.Now()
	if e := (tracking.Persistence{DB: s}).Observe(ctx, 1, r.ID, application.Observation{Stage: "B", CheckedAt: now}); e != nil {
		t.Fatal(e)
	}
	reserved, e := (notifications.Persistence{DB: s}).ReserveDelivery(ctx, 1, now)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := s.History.History(ctx, 1, r.ID)
	s.Close()
	// When
	e = Maintain(ctx, f.db, f.key, func(s *data.Store, _ *credentials.Keyring, _ string) error { return Resume(ctx, s, 1, false) })
	s = f.open(t)
	defer s.Close()
	q, qe := s.Pending.Queue(ctx, 1)
	after, he := s.History.History(ctx, 1, r.ID)
	// Then
	if e != nil || qe != nil || he != nil || len(q) != 1 || q[0].Status == "reserved" || q[0].Status == "delivered" || q[0].CycleAttempts != 0 || q[0].LifetimeAttempts != reserved.LifetimeAttempts || !reflect.DeepEqual(before, after) {
		t.Fatal("resume lost or acknowledged retained work")
	}
	next, e := (notifications.Persistence{DB: s}).ReserveDelivery(ctx, 1, time.Now())
	if e != nil || next.LifetimeAttempts != 2 || next.CycleAttempts != 1 {
		t.Fatal("resume did not preserve lifetime attempt history")
	}
	if e = (notifications.Persistence{DB: s}).CompleteDelivery(ctx, reserved, application.SendOutcome{Category: application.SendAccepted, MessageID: 99}, time.Now()); e == nil {
		t.Fatal("stale attempt acknowledged resumed work")
	}
}
