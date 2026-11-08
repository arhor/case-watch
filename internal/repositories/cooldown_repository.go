package repositories

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"strings"
)

type CooldownRepository struct {
	db DBTX
}

func (this *CooldownRepository) FindBySubjectTypeAndSubjectID(
	ctx context.Context,
	subjectType string,
	subjectID string,
) (Cooldown, error) {
	if !validCooldownKey(subjectType, subjectID) {
		return Cooldown{}, ErrInvalid
	}
	var (
		c       Cooldown
		started int64
	)
	err := this.db.QueryRowContext(
		ctx,
		selectBySubjectTypeAndSubjectID,
		subjectType,
		subjectID,
	).Scan(&c.SubjectType, &c.SubjectID, &started)

	if errors.Is(err, sql.ErrNoRows) {
		return Cooldown{}, ErrNotFound
	}
	if err != nil {
		return Cooldown{}, ErrStorage
	}

	c.StartedAt = time.Unix(0, started).UTC()

	return c, nil
}

func (this *CooldownRepository) Upsert(
	ctx context.Context,
	c Cooldown,
) error {
	if !validCooldownKey(c.SubjectType, c.SubjectID) || c.StartedAt.IsZero() {
		return ErrInvalid
	}
	_, err := this.db.ExecContext(
		ctx,
		insertOrUpdateCooldown,
		c.SubjectType,
		c.SubjectID,
		c.StartedAt.UnixNano(),
	)
	if err != nil {
		return ErrStorage
	}
	return nil
}

func (this *CooldownRepository) DeleteBySubjectTypeAndSubjectID(
	ctx context.Context,
	subjectType,
	subjectID string,
) error {
	if !validCooldownKey(subjectType, subjectID) {
		return ErrInvalid
	}
	_, err := this.db.ExecContext(
		ctx,
		deleteBySubjectTypeAndSubjectID,
		subjectType,
		subjectID,
	)
	if err != nil {
		return ErrStorage
	}
	return nil
}

// ------------------------------ Internal Helpers ------------------------------

const selectBySubjectTypeAndSubjectID = `
    SELECT subject_type, subject_id, started_at
      FROM cooldowns
     WHERE subject_type = ?
       AND subject_id = ?
`

const insertOrUpdateCooldown = `
    INSERT INTO cooldowns (subject_type, subject_id, started_at)
    VALUES (?, ?, ?)
        ON CONFLICT (subject_type, subject_id)
        DO UPDATE
       SET started_at = excluded.started_at
`

const deleteBySubjectTypeAndSubjectID = `
    DELETE FROM cooldowns
     WHERE subject_type = ?
       AND subject_id = ?
`

func validCooldownKey(subjectType, subjectID string) bool {
	return strings.TrimSpace(subjectType) != "" && strings.TrimSpace(subjectID) != ""
}
