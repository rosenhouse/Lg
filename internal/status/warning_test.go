package status_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/status"
)

var now = time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)

// synced is a status whose last good sync was age before now, with sync_interval 10m.
func synced(age time.Duration) *status.Status {
	ok := now.Add(-age)
	return &status.Status{LastSyncOKAt: &ok, SyncIntervalSeconds: 600}
}

func blocked(kind failure.Kind, detail string, retryAt *time.Time) *status.Status {
	st := synced(time.Minute)
	st.Blocked = &status.Blocked{Since: now.Add(-time.Hour), Kind: kind, Detail: detail, RetryAt: retryAt}
	return st
}

var retryAt = now.Add(5 * time.Minute)

var _ = DescribeTable("Warning", Label("status"),
	func(st *status.Status, warning string) {
		Expect(status.Warning(now, st)).To(Equal(warning))
	},
	Entry("never synced", nil, "never synced"),
	Entry("blocked as auth", blocked(failure.Auth, "401 Unauthorized", nil),
		"sync blocked: auth since 2026-10-03T17:00:00Z: 401 Unauthorized"),
	Entry("blocked as rate_limit", blocked(failure.RateLimit, "429 Too Many Requests", &retryAt),
		"sync blocked: rate_limit since 2026-10-03T17:00:00Z, retry_at 2026-10-03T18:05:00Z: 429 Too Many Requests"),
	Entry("blocked as unreachable", blocked(failure.Unreachable, "dial tcp: connection refused", nil),
		"sync blocked: unreachable since 2026-10-03T17:00:00Z: dial tcp: connection refused"),
	Entry("blocked as local_io, on one line", blocked(failure.LocalIO, "write data/x:\n  no space left on device\n", nil),
		"sync blocked: local_io since 2026-10-03T17:00:00Z: write data/x: no space left on device"),
	Entry("blocked as auth, without terminal controls", blocked(failure.Auth, "not logged in\n\x1b[31mhint\x1b[0m:\x7fx\u009b", nil),
		"sync blocked: auth since 2026-10-03T17:00:00Z: not logged in [31mhint [0m: x"),
	Entry("blocked and stale, as blocked", func() *status.Status {
		st := blocked(failure.Auth, "401 Unauthorized", nil)
		st.LastSyncOKAt = nil
		return st
	}(), "sync blocked: auth since 2026-10-03T17:00:00Z: 401 Unauthorized"),
	Entry("with no good sync yet", &status.Status{SyncIntervalSeconds: 600}, "no sync has succeeded yet"),
	Entry("stale beyond twice sync_interval", synced(20*time.Minute+time.Second),
		"last successful sync was 20m1s ago, at 2026-10-03T17:39:59Z, over twice sync_interval 10m0s"),
	Entry("at twice sync_interval", synced(20*time.Minute), ""),
	Entry("fresh", synced(time.Minute), ""),
	Entry("with units pending beyond twice sync_interval", pendingSince(now.Add(-time.Minute), now.Add(-21*time.Minute), now.Add(-30*time.Minute)),
		"2 units pending since 2026-10-03T17:30:00Z; run `lg status`"),
	Entry("with units pending for twice sync_interval", pendingSince(now.Add(-20*time.Minute)), ""),
	Entry("stale and with units pending, as stale", func() *status.Status {
		st := pendingSince(now.Add(-time.Hour))
		st.LastSyncOKAt = ptr(now.Add(-time.Hour))
		return st
	}(), "last successful sync was 1h0m0s ago, at 2026-10-03T17:00:00Z, over twice sync_interval 10m0s"),
)

// pendingSince is a fresh status with a unit pending since each time.
func pendingSince(times ...time.Time) *status.Status {
	st := synced(time.Minute)
	var pending []status.Pending
	for i, since := range times {
		pending = append(pending, status.Pending{Unit: status.Unit{Run: int64(i + 1)}, Error: "502", Since: since})
	}
	st.Repos = map[string]status.Repo{repo: {Pending: pending}}
	return st
}
