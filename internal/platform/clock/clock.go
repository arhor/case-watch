package clock

import (
	"time"

	"github.com/arhor/case-watch/internal/domain"
)

type RealClock struct{}

func (this RealClock) Now() time.Time {
	return time.Now()
}

func NewClock() domain.Clock {
	return RealClock{}
}
