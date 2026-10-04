// Package clock is the only place lg reads the time.
package clock

import (
	"fmt"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type Real struct{}

func (Real) Now() time.Time { return time.Now() }

func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake is a clock that moves only when Set.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	deadline time.Time
	fired    chan time.Time
}

func NewFake(now time.Time) *Fake { return &Fake{now: now} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set moves the clock to now and fires every After whose deadline it reaches.
func (f *Fake) Set(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
	waiting := f.waiters[:0]
	for _, w := range f.waiters {
		if now.Before(w.deadline) {
			waiting = append(waiting, w)
			continue
		}
		w.fired <- now
	}
	f.waiters = waiting
}

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := waiter{deadline: f.now.Add(d), fired: make(chan time.Time, 1)}
	if d <= 0 {
		w.fired <- f.now
		return w.fired
	}
	f.waiters = append(f.waiters, w)
	return w.fired
}

// FromEnv gives base, or, when LG_TEST_NOW is set, a clock that starts at
// that RFC 3339 time and advances as base does.
func FromEnv(env map[string]string, base Clock) (Clock, error) {
	value := env["LG_TEST_NOW"]
	if value == "" {
		return base, nil
	}
	start, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("LG_TEST_NOW: %w", err)
	}
	return Starting(start, base), nil
}

// Starting gives a clock that starts at start and advances as base does.
func Starting(start time.Time, base Clock) Clock {
	return shifted{Clock: base, start: start, baseStart: base.Now()}
}

// shifted adds base's elapsed time to start. Storing start minus base as a
// time.Duration would overflow when they are centuries apart.
type shifted struct {
	Clock
	start, baseStart time.Time
}

func (s shifted) Now() time.Time { return s.start.Add(s.Clock.Now().Sub(s.baseStart)) }
