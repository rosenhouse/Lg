package daemon

import (
	"context"
	"io"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
)

// Outcome is what a Loop needs to know of a cycle.
type Outcome struct {
	Started  time.Time
	Interval time.Duration
	RetryAt  time.Time
	Err      error
}

type Loop struct {
	Clock     clock.Clock
	Cycle     func(ctx context.Context, served int64) Outcome
	Requested func() (int64, error)
	Reconcile func(ctx context.Context) error
	Log       io.Writer
}

func (l *Loop) Run(ctx context.Context) {}
