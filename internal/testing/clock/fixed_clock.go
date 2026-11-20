package clock

import (
	"sync"
	"time"

	"github.com/arhor/case-watch/internal/models"
)

var _ models.Clock = (*FixedClock)(nil)

// FixedClock returns a fixed time until explicitly changed with Set.
// Its methods are safe for concurrent use.
type FixedClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewFixedClock(now time.Time) *FixedClock {
	return &FixedClock{now: now}
}

func (this *FixedClock) Now() time.Time {
	this.mu.Lock()
	defer this.mu.Unlock()

	return this.now
}

func (this *FixedClock) Set(now time.Time) {
	this.mu.Lock()
	defer this.mu.Unlock()

	this.now = now
}
