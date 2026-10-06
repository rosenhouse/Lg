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

func withConfigError(st *status.Status, err string) *status.Status {
	st.ConfigError = &err
	return st
}

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
	Entry("fresh, with an invalid config.yaml the daemon kept the last good one for", withConfigError(synced(time.Minute), "config.yaml: unknown key colour;\nkept the last good config"),
		"config.yaml is invalid: config.yaml: unknown key colour; kept the last good config"),
	Entry("stale, with an invalid config.yaml, as stale", withConfigError(synced(time.Hour), "unknown key colour"),
		"last successful sync was 1h0m0s ago, at 2026-10-03T17:00:00Z, over twice sync_interval 10m0s"),
	Entry("blocked, with an invalid config.yaml, as blocked", withConfigError(blocked(failure.Auth, "401 Unauthorized", nil), "unknown key colour"),
		"sync blocked: auth since 2026-10-03T17:00:00Z: 401 Unauthorized"),
	Entry("fresh, with a sync_interval whose double overflows", func() *status.Status {
		st := synced(time.Minute)
		st.SyncIntervalSeconds = 2_000_000 * 3600
		return st
	}(), ""),
)
