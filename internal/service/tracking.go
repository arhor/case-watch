package service

import (
	"context"
	"errors"
	"github.com/arhor/case-watch/internal/application"
	"github.com/arhor/case-watch/internal/storage"
	"github.com/arhor/case-watch/internal/tracking"
)

// monitoredTracking propagates infrastructure failures to the process owner;
// ordinary portal/user failures remain local to the request.
type monitoredTracking struct {
	*tracking.Coordinator
	fail func()
}

func (m monitoredTracking) checkError(e error) {
	if errors.Is(e, tracking.ErrStorage) || errors.Is(e, tracking.ErrCredentials) {
		m.fail()
	}
}
func (m monitoredTracking) Enroll(c context.Context, o int64, p application.Credentials) (application.Observation, error) {
	v, e := m.Coordinator.Enroll(c, o, p)
	m.checkError(e)
	return v, e
}
func (m monitoredTracking) Status(c context.Context, o int64) (application.Status, error) {
	v, e := m.Coordinator.Status(c, o)
	m.checkError(e)
	return v, e
}
func (m monitoredTracking) Scheduled(c context.Context, o int64) (application.Status, error) {
	v, e := m.Coordinator.Scheduled(c, o)
	m.checkError(e)
	return v, e
}
func (m monitoredTracking) Remove(c context.Context, o int64) error {
	e := m.Coordinator.Remove(c, o)
	m.checkError(e)
	return e
}
func (m monitoredTracking) RunOwnerWork(c context.Context, o int64, id application.EnrollmentID, fn func(context.Context) error) error {
	e := m.Coordinator.RunOwnerWork(c, o, id, fn)
	m.checkError(e)
	return e
}

type monitoredReader struct {
	s    *storage.Store
	fail func()
}

func (m monitoredReader) Enrollment(c context.Context, o int64) (storage.Enrollment, error) {
	v, e := m.s.Enrollment(c, o)
	if e != nil && c.Err() == nil && !errors.Is(e, storage.ErrNotFound) {
		m.fail()
	}
	return v, e
}
