// Package daemon runs a cycle every sync_interval, and at each sync request.
package daemon

import "time"

// Next is when the cycle after o is due: one interval after its start's
// second, or at RetryAt when that is later. It carries no monotonic reading,
// which stops while a laptop sleeps.
func (o Outcome) Next() time.Time {
	next := o.Started.Truncate(time.Second).Add(o.Interval)
	if o.RetryAt.After(next) {
		return o.RetryAt.Round(0)
	}
	return next
}
