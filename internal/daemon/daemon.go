// Package daemon runs a cycle every sync_interval, and at each sync request.
package daemon

import "time"

// Next is when the cycle after one that started at started is due: one
// interval later, or at retryAt when that is later.
func Next(started time.Time, interval time.Duration, retryAt time.Time) time.Time {
	next := started.Add(interval)
	if retryAt.After(next) {
		return retryAt
	}
	return next
}
