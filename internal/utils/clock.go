package utils

import "time"

type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func NewClock() Clock {
	return RealClock{}
}

func (RealClock) Now() time.Time {
	return time.Now()
}
