// Package clock is the only place lg reads the time.
package clock

import "time"

type Clock interface {
	After(d time.Duration) <-chan time.Time
}

type Real struct{}

func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }
