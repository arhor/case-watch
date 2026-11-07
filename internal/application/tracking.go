package application

import (
	"context"
	"errors"

	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/services/tracking"
)

// monitoredTracking propagates infrastructure failures to the process owner;
// ordinary portal/user failures remain local to the request.
type monitoredTracking struct {
	*tracking.Coordinator
	fail func()
}

func (m monitoredTracking) checkError(ctx context.Context, e error) {
	if ctx.Err() != nil {
		return
	}
	if errors.Is(e, tracking.ErrStorage) {
		m.fail()
	}
}
func (m monitoredTracking) Enroll(c context.Context, o int64, p domain.Credentials) (domain.Observation, error) {
	v, e := m.Coordinator.Enroll(c, o, p)
	m.checkError(c, e)
	return v, e
}
func (m monitoredTracking) Status(c context.Context, o int64) (domain.Status, error) {
	v, e := m.Coordinator.Status(c, o)
	m.checkError(c, e)
	return v, e
}
func (m monitoredTracking) HasEnrollment(c context.Context, o int64) (bool, error) {
	v, e := m.Coordinator.HasEnrollment(c, o)
	m.checkError(c, e)
	return v, e
}
func (m monitoredTracking) ScheduledOwners(c context.Context) ([]int64, error) {
	v, e := m.Coordinator.ScheduledOwners(c)
	m.checkError(c, e)
	return v, e
}
func (m monitoredTracking) Scheduled(c context.Context, o int64) (domain.Status, error) {
	v, e := m.Coordinator.Scheduled(c, o)
	m.checkError(c, e)
	return v, e
}
func (m monitoredTracking) Remove(c context.Context, o int64) error {
	e := m.Coordinator.Remove(c, o)
	m.checkError(c, e)
	return e
}
func (m monitoredTracking) RunOwnerWork(c context.Context, o int64, id string, fn func(context.Context) error) error {
	e := m.Coordinator.RunOwnerWork(c, o, id, fn)
	m.checkError(c, e)
	return e
}
