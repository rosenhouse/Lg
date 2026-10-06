package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/status"
)

// waitPoll is how often WaitForCycle reads status.json.
const waitPoll = 100 * time.Millisecond

// WaitForCycle waits up to timeout from since for status.json to show a
// finished cycle that started at or after since and served sync request n,
// and gives that cycle's result. It fails at once when the daemon exits
// first, or when the daemon is blocked past the timeout.
func WaitForCycle(state string, n int64, since time.Time, timeout time.Duration, clk clock.Clock) error {
	deadline := since.Add(timeout)
	// status.json holds whole seconds.
	since = since.Truncate(time.Second)
	timedOut := clk.After(deadline.Sub(clk.Now()))
	for {
		// A daemon writes status.json before it lets go of daemon.lock, so
		// reading status.json second never misses a cycle it served.
		running, err := Running(state)
		if err != nil {
			return err
		}
		st, err := status.Read(filepath.Join(state, "status.json"))
		// A running daemon rewrites a status.json that does not parse, but
		// cannot repair one it cannot read.
		var unreadable *fs.PathError
		if err != nil && (!running || errors.As(err, &unreadable)) {
			return err
		}
		switch {
		case st != nil && st.ServedRequest >= n && !st.LastSyncStartedAt.Before(since):
			return result(st)
		case !running:
			return fmt.Errorf("the daemon exited before it served sync request %d", n)
		case st != nil && st.Blocked != nil && st.Blocked.RetryAt != nil && st.Blocked.RetryAt.After(deadline):
			return blocked(*st.Blocked)
		}
		select {
		case <-timedOut:
			return fmt.Errorf("no cycle served sync request %d; %w after %s", n, lock.ErrTimeout, timeout)
		case <-clk.After(waitPoll):
		}
	}
}

// result is the error a one-shot sync would give for the cycle st records.
func result(st *status.Status) error {
	if st.Blocked != nil {
		return blocked(*st.Blocked)
	}
	errs := make([]error, len(st.LastSyncErrors))
	for i, line := range st.LastSyncErrors {
		errs[i] = errors.New(line)
	}
	return errors.Join(errs...)
}

func blocked(b status.Blocked) failure.Blocked {
	retryAt := time.Time{}
	if b.RetryAt != nil {
		retryAt = *b.RetryAt
	}
	return failure.Blocked{Kind: b.Kind, Detail: b.Detail, RetryAt: retryAt}
}
