package app

import (
	"context"
	"testing"

	"github.com/arhor/case-watch/internal/services/tracking"
)

func TestMonitoredTrackingShouldIgnoreStorageFailureWhenOperationIsCanceled(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failed := false
	monitor := monitoredTracking{fail: func() { failed = true }}
	// When
	monitor.checkError(ctx, tracking.ErrStorage)
	// Then
	if failed {
		t.Fatal("canceled operation stopped the service")
	}
}
func TestMonitoredTrackingShouldReportStorageFailureWhenOperationIsActive(t *testing.T) {
	// Given
	failed := false
	monitor := monitoredTracking{fail: func() { failed = true }}
	// When
	monitor.checkError(context.Background(), tracking.ErrStorage)
	// Then
	if !failed {
		t.Fatal("active storage failure was not reported")
	}
}
