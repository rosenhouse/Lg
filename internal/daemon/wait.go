package daemon

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/status"
)

// waitPoll is how often WaitForCycle reads status.json.
const waitPoll = 100 * time.Millisecond

// WaitForCycle waits up to timeout for status.json to show a finished cycle
// that served sync request n, and gives that cycle's result. It fails at
// once when the daemon exits first, or when the daemon is blocked past the
// timeout.
func WaitForCycle(state string, n int64, timeout time.Duration, clk clock.Clock) error {
	deadline := clk.Now().Add(timeout)
	timedOut := clk.After(timeout)
	for {
		// A daemon writes status.json before it lets go of daemon.lock, so
		// reading status.json second never misses a cycle it served.
		running, err := Running(state)
		if err != nil {
			return err
		}
		st, err := status.Read(filepath.Join(state, "status.json"))
		if err != nil {
			return err
		}
		switch {
		case st != nil && st.ServedRequest >= n:
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
	if st.LastSyncOKAt == nil || st.LastSyncOKAt.Before(st.LastSyncFinishedAt) {
		return fmt.Errorf("the daemon's sync at %s failed; its log says why", st.LastSyncStartedAt.Format(time.RFC3339))
	}
	var pending []error
	for _, repo := range slices.Sorted(maps.Keys(st.Repos)) {
		for _, p := range st.Repos[repo].Pending {
			pending = append(pending, errors.New(p.String()))
		}
	}
	return errors.Join(pending...)
}

func blocked(b status.Blocked) failure.Blocked {
	retryAt := time.Time{}
	if b.RetryAt != nil {
		retryAt = *b.RetryAt
	}
	return failure.Blocked{Kind: b.Kind, Detail: b.Detail, RetryAt: retryAt}
}
