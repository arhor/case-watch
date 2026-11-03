package service

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/data"
)

func TestEnrollmentShouldRejectInvalidCredentialsWhenCreatingOrUpdating(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invalidate func(*application.SealedCredentials)
	}{
		{"invalid identity", func(c *application.SealedCredentials) { c.ID = "invalid" }},
		{"unsupported version", func(c *application.SealedCredentials) { c.FormatVersion = 2 }},
		{"empty key", func(c *application.SealedCredentials) { c.KeyID = "" }},
		{"blank key", func(c *application.SealedCredentials) { c.KeyID = " \t\n\u00a0" }},
		{"nil payload", func(c *application.SealedCredentials) { c.Payload = nil }},
		{"empty payload", func(c *application.SealedCredentials) { c.Payload = []byte{} }},
		{"short payload", func(c *application.SealedCredentials) { c.Payload = make([]byte, 27) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			s, _, _, c := persistenceFixture(t)
			invalid := c
			tc.invalidate(&invalid)
			// When
			createErr := s.Enrollments.Create(ctx, 101, invalid, observation("A", 0))
			// Then
			if !errors.Is(createErr, data.ErrInvalid) || count(t, s, "enrollments") != 0 {
				t.Fatalf("invalid creation: %v", createErr)
			}
			enroll(t, s, c)
			// When
			updateErr := s.Enrollments.UpdateCredentials(ctx, invalid)
			// Then
			got, err := s.Enrollments.FindByOwner(ctx, 101)
			if !errors.Is(updateErr, data.ErrInvalid) || err != nil || got.Credentials.KeyID != c.KeyID || got.Credentials.FormatVersion != c.FormatVersion || !bytes.Equal(got.Credentials.Payload, c.Payload) {
				t.Fatalf("invalid update: %v; read: %v", updateErr, err)
			}
		})
	}
}

func TestEnrollmentShouldRejectInvalidInputWhenWritingOwnerOrObservation(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	for _, owner := range []int64{0, -1} {
		// When / Then
		if err := s.Enrollments.Create(ctx, owner, c, observation("A", 0)); !errors.Is(err, data.ErrInvalid) {
			t.Fatalf("owner %d: %v", owner, err)
		}
	}
	if err := s.Enrollments.Create(ctx, 101, c, application.Observation{}); !errors.Is(err, data.ErrInvalid) {
		t.Fatalf("missing timestamp: %v", err)
	}
	if count(t, s, "enrollments") != 0 {
		t.Fatal("invalid input created enrollment")
	}
	enroll(t, s, c)
	for _, owner := range []int64{0, -1} {
		if err := s.Enrollments.UpdateObservation(ctx, owner, c.ID, observation("B", 1)); !errors.Is(err, data.ErrInvalid) {
			t.Fatal(err)
		}
		if err := s.Enrollments.SoftDelete(ctx, owner, c.ID, epoch); !errors.Is(err, data.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := s.Enrollments.UpdateObservation(ctx, 101, c.ID, application.Observation{}); !errors.Is(err, data.ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.Enrollments.SoftDelete(ctx, 101, c.ID, time.Time{}); !errors.Is(err, data.ErrInvalid) {
		t.Fatal(err)
	}
	got, err := s.Enrollments.FindByOwner(ctx, 101)
	if err != nil || got.Observation.Stage != "A" || !got.Observation.CheckedAt.Equal(epoch) {
		t.Fatalf("invalid writes altered baseline: %v", err)
	}
}

func TestPendingShouldRejectInvalidIDsWhenCreatingDelivery(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	sequence, err := s.History.Append(ctx, c.ID, "A", observation("B", 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][2]int64{{sequence, 0}, {sequence, -1}, {0, 101}, {-1, 101}} {
		// When
		err := s.Pending.Create(ctx, ids[0], ids[1])
		// Then
		if !errors.Is(err, data.ErrInvalid) {
			t.Fatalf("invalid IDs %v: %v", ids, err)
		}
	}
	if count(t, s, "pending") != 0 || count(t, s, "recipients") != 0 {
		t.Fatal("invalid delivery created rows")
	}
	if err := s.Pending.Create(ctx, sequence, 101); err != nil {
		t.Fatal(err)
	}
	deliveries, err := s.Pending.FindByRecipient(ctx, 101)
	if err != nil || len(deliveries) != 1 || count(t, s, "recipients") != 1 {
		t.Fatalf("valid delivery: %v", err)
	}
}

func TestEnrollmentShouldPreserveStateInvariantWhenUpdatedAndSoftDeleted(t *testing.T) {
	// Given
	s, _, _, c := persistenceFixture(t)
	enroll(t, s, c)
	rotated := c
	rotated.KeyID = "rotated"
	rotated.Payload = bytes.Repeat([]byte{1}, 28)
	// When
	if err := s.Enrollments.UpdateCredentials(ctx, rotated); err != nil {
		t.Fatal(err)
	}
	if err := s.Enrollments.UpdateObservation(ctx, 101, c.ID, observation("B", 1)); err != nil {
		t.Fatal(err)
	}
	// Then
	got, err := s.Enrollments.FindByOwner(ctx, 101)
	if err != nil || got.Credentials.KeyID != rotated.KeyID || !bytes.Equal(got.Credentials.Payload, rotated.Payload) || got.Observation.Stage != "B" {
		t.Fatalf("active state: %v", err)
	}
	// When
	if err := s.Enrollments.SoftDelete(ctx, 101, c.ID, epoch); err != nil {
		t.Fatal(err)
	}
	// Then
	if err := s.Enrollments.UpdateCredentials(ctx, rotated); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("deleted credential update: %v", err)
	}
	if err := s.Enrollments.UpdateObservation(ctx, 101, c.ID, observation("C", 2)); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("deleted observation update: %v", err)
	}
	var scrubbed bool
	err = s.db.QueryRow(`SELECT owner_id = 101 AND deleted_at IS NOT NULL
 AND credential_version IS NULL AND key_id IS NULL AND payload IS NULL
 AND stage IS NULL AND checked_at IS NULL FROM enrollments WHERE id = ?`, c.ID).Scan(&scrubbed)
	if err != nil || !scrubbed {
		t.Fatalf("deleted state not scrubbed: %v", err)
	}
}
