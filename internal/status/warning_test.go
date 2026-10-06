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

// pendingFor gives st with one pending unit per age in each repo, pending
// since that long before now, or with a zero since for a zero age.
func pendingFor(st *status.Status, repos map[string][]time.Duration) *status.Status {
	st.Repos = map[string]status.Repo{}
	for name, ages := range repos {
		var r status.Repo
		for i, age := range ages {
			since := time.Time{}
			if age != 0 {
				since = now.Add(-age)
			}
			r.Pending = append(r.Pending, status.Pending{Unit: status.Unit{Run: int64(i + 1)}, Error: "503", Since: since})
		}
		st.Repos[name] = r
	}
	return st
}

func withConfigError(st *status.Status) *status.Status {
	msg := "config.yaml: sync_interval must be at least 1m: 30s; kept the last good config"
	st.ConfigError = &msg
	return st
}

const stuckLine = "4 units pending since 2026-10-03T17:30:00Z; run `lg status`"

var stuck = map[string][]time.Duration{
	"github.com/rosenhouse/lg": {21 * time.Minute, 30 * time.Minute, 5 * time.Minute, 25 * time.Minute},
	"ghe.example.com/o/r":      {22 * time.Minute, 0},
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
	Entry("with units pending longer than twice sync_interval, counting only those", pendingFor(synced(time.Minute), stuck), stuckLine),
	Entry("with a unit pending for twice sync_interval", pendingFor(synced(time.Minute), map[string][]time.Duration{"r": {20 * time.Minute}}), ""),
	Entry("with a unit pending for less than twice sync_interval", pendingFor(synced(time.Minute), map[string][]time.Duration{"r": {15 * time.Minute}}), ""),
	Entry("with a unit pending since a zero time", pendingFor(synced(time.Minute), map[string][]time.Duration{"r": {0}}), ""),
	Entry("blocked with stuck units, as blocked", pendingFor(blocked(failure.Auth, "401 Unauthorized", nil), stuck),
		"sync blocked: auth since 2026-10-03T17:00:00Z: 401 Unauthorized"),
	Entry("with no good sync yet and stuck units, as no good sync", pendingFor(&status.Status{SyncIntervalSeconds: 600}, stuck), "no sync has succeeded yet"),
	Entry("stale with stuck units, as stale", pendingFor(synced(30*time.Minute), stuck),
		"last successful sync was 30m0s ago, at 2026-10-03T17:30:00Z, over twice sync_interval 10m0s"),
	Entry("with an invalid config.yaml", withConfigError(synced(time.Minute)),
		"config.yaml is invalid: config.yaml: sync_interval must be at least 1m: 30s; kept the last good config"),
	Entry("stale with an invalid config.yaml, as stale", withConfigError(synced(30*time.Minute)),
		"last successful sync was 30m0s ago, at 2026-10-03T17:30:00Z, over twice sync_interval 10m0s"),
	Entry("with stuck units and an invalid config.yaml, as stuck", withConfigError(pendingFor(synced(time.Minute), stuck)), stuckLine),
	Entry("fresh, with a sync_interval whose double overflows", func() *status.Status {
		st := synced(time.Minute)
		st.SyncIntervalSeconds = 2_000_000 * 3600
		return st
	}(), ""),
)
