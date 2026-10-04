// Package clock is the only place lg reads the time.
package clock

import "time"

type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type Real struct{}

func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (Real) Now() time.Time { return time.Time{} }

// Fake is a clock that moves only when Set.
type Fake struct{}

func NewFake(time.Time) *Fake { return &Fake{} }

func (*Fake) Now() time.Time { return time.Time{} }

func (*Fake) Set(time.Time) {}

func (*Fake) After(time.Duration) <-chan time.Time { return nil }

func FromEnv(map[string]string, Clock) (Clock, error) { return nil, nil }
