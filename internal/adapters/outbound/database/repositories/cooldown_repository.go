package repositories

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/arhor/case-watch/internal/domain"
)

var _ domain.CooldownRepository = (*CooldownRepository)(nil)

type CooldownRepository struct {
	executor QueryExecutor
}

func NewCooldownRepository(executor QueryExecutor) *CooldownRepository {
	return &CooldownRepository{executor: executor}
}

func (this *CooldownRepository) FindBySubjectTypeAndSubjectID(
	ctx context.Context,
	subjectType string,
	subjectID string,
) (domain.Cooldown, error) {
	if !validCooldownKey(subjectType, subjectID) {
		return domain.Cooldown{}, ErrInvalid
	}
	var (
		c       domain.Cooldown
		started int64
	)
	err := this.executor.QueryRowContext(
		ctx,
		selectBySubjectTypeAndSubjectID,
		subjectType,
		subjectID,
	).Scan(&c.SubjectType, &c.SubjectID, &started)

	if errors.Is(err, sql.ErrNoRows) {
		return domain.Cooldown{}, ErrNotFound
	}
	if err != nil {
		return domain.Cooldown{}, ErrStorage
	}

	c.StartedAt = time.Unix(0, started).UTC()

	return c, nil
}

func (this *CooldownRepository) Upsert(
	ctx context.Context,
	c domain.Cooldown,
) error {
	if !validCooldownKey(c.SubjectType, c.SubjectID) || c.StartedAt.IsZero() {
		return ErrInvalid
	}
	_, err := this.executor.ExecContext(
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
	_, err := this.executor.ExecContext(
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

// -- Internals ----------------------------------------------------------------
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
