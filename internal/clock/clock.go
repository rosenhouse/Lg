// Package clock is the only place lg reads the time.
package clock

import "time"

type Clock interface {
	After(d time.Duration) <-chan time.Time
}

type Real struct{}

func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake is a clock that moves only when Set.
type Fake struct{}

func NewFake(time.Time) *Fake { return &Fake{} }

func (*Fake) Now() time.Time { return time.Time{} }

func (*Fake) Set(time.Time) {}

func (*Fake) After(time.Duration) <-chan time.Time { return nil }
