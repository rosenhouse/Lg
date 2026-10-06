package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/status"
)

// pollInterval is how often a Loop reads the sync request count while it waits.
const pollInterval = time.Second

// maxLogBytes is the size past which a Loop empties the file it logs to.
const maxLogBytes = 10_000_000

// Outcome is what a Loop needs to know of a cycle. RetryAt is zero unless
// the cycle blocked until then.
type Outcome struct {
	Started  time.Time
	Interval time.Duration
	RetryAt  time.Time
	Err      error
	// Skipped is whether the cycle did not run, so it served no request.
	Skipped bool
}

// Loop runs Cycle at once, and then when each Outcome says the next is due, or when
// a sync request comes. After each cycle it reconciles the index and logs one
// line.
type Loop struct {
	Clock clock.Clock
	// RetryAt defers the first cycle, as a restarted daemon's blocked.retry_at does.
	RetryAt time.Time
	// Cycle runs one cycle. Just before a cycle starts, it calls serving,
	// which gives the latest sync request, and then serves the requests up
	// to that one.
	Cycle func(ctx context.Context, serving func() int64) Outcome
	// Requested gives the number of the latest sync request.
	Requested func() (int64, error)
	// Lost gives an error once this daemon no longer holds the instance lock.
	Lost      func() error
	Reconcile func(ctx context.Context) error
	// Log is stderr, which launchd sends to a file.
	Log io.Writer
}

// Run returns when ctx is done, after any cycle in progress returns, or
// with Lost's error.
func (l *Loop) Run(ctx context.Context) error {
	if err := l.wait(ctx, 0, l.RetryAt, l.RetryAt); err != nil {
		return err
	}
	var served int64
	for ctx.Err() == nil {
		l.truncateLog()
		serving := served
		out := l.Cycle(ctx, func() int64 {
			if n, err := l.Requested(); err != nil {
				l.logf("%s", err)
			} else {
				serving = n
			}
			return serving
		})
		if ctx.Err() != nil {
			l.logf("stopped")
			return nil
		}
		result := "ok"
		if out.Err != nil {
			result = status.OneLine(out.Err.Error())
		}
		next := out.Next()
		l.logf("sync at %s: %s; next sync at %s", out.Started.UTC().Format(time.RFC3339), result, next.UTC().Format(time.RFC3339))
		if err := l.Reconcile(ctx); err != nil {
			l.logf("reconcile lg.db: %s", err)
		}
		retryAt := out.RetryAt
		if !out.Skipped {
			served = serving
		} else if soonest := l.Clock.Now().Add(pollInterval); soonest.After(retryAt) {
			retryAt = soonest
		}
		if err := l.wait(ctx, served, next, retryAt); err != nil {
			return err
		}
	}
	return nil
}

// wait waits until next, or, once a request after served comes, until
// retryAt, or until ctx is done or Lost gives an error.
func (l *Loop) wait(ctx context.Context, served int64, next, retryAt time.Time) error {
	for {
		if err := l.Lost(); err != nil {
			return err
		}
		if n, err := l.Requested(); err == nil && n > served {
			next = retryAt
		}
		now := l.Clock.Now()
		if !now.Before(next) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-l.Clock.After(min(pollInterval, next.Sub(now))):
		}
	}
}

func (l *Loop) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(l.Log, "lg: "+format+"\n", args...)
}

// truncateLog empties Log when it is a file over maxLogBytes.
func (l *Loop) truncateLog() {
	f, ok := l.Log.(*os.File)
	if !ok {
		return
	}
	info, err := f.Stat()
	if err != nil || info.Size() <= maxLogBytes {
		return
	}
	if err := f.Truncate(0); err != nil {
		l.logf("truncate the log: %s", err)
		return
	}
	_, _ = f.Seek(0, io.SeekStart)
}
