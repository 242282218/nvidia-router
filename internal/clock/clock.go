package clock

import "time"

type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) *time.Timer
	AfterFunc(time.Duration, func()) *time.Timer
}

type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now()
}

func (RealClock) NewTimer(duration time.Duration) *time.Timer {
	return time.NewTimer(duration)
}

func (RealClock) AfterFunc(duration time.Duration, callback func()) *time.Timer {
	return time.AfterFunc(duration, callback)
}
