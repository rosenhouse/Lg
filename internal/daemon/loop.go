package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
)

// PollInterval is how often a Loop reads the sync request count while it waits.
const PollInterval = time.Second

// maxLogBytes is the size past which a Loop empties the file it logs to.
const maxLogBytes = 10_000_000

// Outcome is what a Loop needs to know of a cycle. RetryAt is zero unless
// the cycle blocked until then.
type Outcome struct {
	Started  time.Time
	Interval time.Duration
	RetryAt  time.Time
	Err      error
}

// Loop runs Cycle at once, and then whenever Next says or a sync request
// comes. After each cycle it reconciles the index and logs one line.
type Loop struct {
	Clock clock.Clock
	// Cycle runs one cycle that serves sync requests up to served.
	Cycle func(ctx context.Context, served int64) Outcome
	// Requested gives the number of the latest sync request.
	Requested func() (int64, error)
	Reconcile func(ctx context.Context) error
	// Log is stderr, which launchd sends to a file.
	Log io.Writer
}

// Run returns when ctx is done, after any cycle in progress returns.
func (l *Loop) Run(ctx context.Context) {
	var served int64
	for {
		if n, err := l.Requested(); err != nil {
			l.logf("%s", err)
		} else {
			served = max(served, n)
		}
		l.truncateLog()
		out := l.Cycle(ctx, served)
		next := Next(out.Started, out.Interval, out.RetryAt)
		result := "ok"
		if out.Err != nil {
			result = strings.Join(strings.Fields(out.Err.Error()), " ")
		}
		l.logf("sync at %s: %s; next sync at %s", out.Started.UTC().Format(time.RFC3339), result, next.UTC().Format(time.RFC3339))
		if ctx.Err() != nil {
			return
		}
		if err := l.Reconcile(ctx); err != nil {
			l.logf("reconcile lg.db: %s", err)
		}
		if !l.wait(ctx, served, next, out.RetryAt) {
			return
		}
	}
}

// wait waits until next, or, once a request after served comes, until
// retryAt. It reports false when ctx is done first.
func (l *Loop) wait(ctx context.Context, served int64, next, retryAt time.Time) bool {
	for {
		if n, err := l.Requested(); err == nil && n > served {
			next = retryAt
		}
		now := l.Clock.Now()
		if !now.Before(next) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-l.Clock.After(min(PollInterval, next.Sub(now))):
		}
	}
}

func (l *Loop) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(l.Log, "lg: "+format+"\n", args...)
}

// truncateLog empties Log when it is a regular file over maxLogBytes.
func (l *Loop) truncateLog() {
	f, ok := l.Log.(*os.File)
	if !ok {
		return
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= maxLogBytes {
		return
	}
	if err := f.Truncate(0); err != nil {
		l.logf("truncate the log: %s", err)
		return
	}
	_, _ = f.Seek(0, io.SeekStart)
}
