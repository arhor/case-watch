package service

import (
	"context"
	"errors"

	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/data"
	"github.com/arhor/case-watch/internal/tracking"
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
func (m monitoredTracking) Enroll(c context.Context, o int64, p application.Credentials) (application.Observation, error) {
	v, e := m.Coordinator.Enroll(c, o, p)
	m.checkError(c, e)
	return v, e
}
func (m monitoredTracking) Status(c context.Context, o int64) (application.Status, error) {
	v, e := m.Coordinator.Status(c, o)
	m.checkError(c, e)
	return v, e
}
func (m monitoredTracking) Scheduled(c context.Context, o int64) (application.Status, error) {
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

type monitoredReader struct {
	s    *data.Store
	fail func()
}

func (m monitoredReader) FindByOwner(c context.Context, o int64) (data.Enrollment, error) {
	v, e := m.s.Enrollments.FindByOwner(c, o)
	if e != nil && c.Err() == nil && !errors.Is(e, data.ErrNotFound) {
		m.fail()
	}
	return v, e
}
