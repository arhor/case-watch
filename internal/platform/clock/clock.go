package clock

import (
	"time"

	"github.com/arhor/case-watch/internal/models"
)

type RealClock struct{}

func (this RealClock) Now() time.Time {
	return time.Now()
}

func NewClock() models.Clock {
	return RealClock{}
}
