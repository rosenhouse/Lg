// Package daemon runs a cycle every sync_interval, and at each sync request.
package daemon

import "time"

// Next is when the cycle after one that started at started is due: one
// interval after its second, or at retryAt when that is later. It carries no
// monotonic reading, which stops while a laptop sleeps.
func Next(started time.Time, interval time.Duration, retryAt time.Time) time.Time {
	next := started.Truncate(time.Second).Add(interval)
	if retryAt.After(next) {
		return retryAt.Round(0)
	}
	return next
}
