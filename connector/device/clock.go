package device

import "time"

// Clock is the time a Device schedules against: the end of a pulse and the watchdog.
// Tests drive it by hand, so no test of this package waits on the wall clock.
type Clock interface {
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a scheduled call that may still be cancelled.
type Timer interface {
	Stop() bool
}

// SystemClock is the wall clock.
type SystemClock struct{}

// AfterFunc schedules f on the wall clock.
func (SystemClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
