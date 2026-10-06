// Package daemon runs a cycle every sync_interval, and at each sync request.
package daemon

import "time"

func Next(started time.Time, interval time.Duration, retryAt time.Time) time.Time {
	return time.Time{}
}
