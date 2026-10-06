package status_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/status"
)

const repo = "github.com/rosenhouse/lg"

var newest = time.Date(2026, 10, 3, 14, 22, 54, 0, time.UTC)

// good is a cycle that started at started, took 90.5s, and completed.
func good(started time.Time) status.Cycle {
	return status.Cycle{
		Started:       started,
		Finished:      started.Add(90*time.Second + 500*time.Millisecond),
		Completed:     true,
		Repo:          repo,
		DefaultBranch: "main",
		SyncInterval:  10 * time.Minute,
		Retention:     90 * 24 * time.Hour,
		DiskCap:       50e9,
		Disk:          status.Disk{Runs: 3, Attempts: 4, Bytes: 1234, NewestCompleted: newest},
	}
}

func blockedCycle(started time.Time, b failure.Blocked) status.Cycle {
	c := good(started)
	c.Completed, c.Err, c.DefaultBranch = false, fmt.Errorf("sync: %w", b), ""
	return c
}

func ptr[T any](v T) *T { return &v }

var _ = Describe("Next", Label("status"), func() {
	started := time.Date(2026, 10, 3, 18, 0, 0, 0, time.FixedZone("PDT", -7*3600))
	finished := time.Date(2026, 10, 4, 1, 1, 30, 0, time.UTC)

	It("records a first good cycle", func() {
		Expect(status.Next(nil, good(started))).To(Equal(status.Status{
			LgFormat:            1,
			Cycle:               1,
			LastSyncStartedAt:   started.UTC(),
			LastSyncFinishedAt:  finished,
			LastSyncOKAt:        &finished,
			SyncIntervalSeconds: 600,
			Repos: map[string]status.Repo{repo: {
				DefaultBranch:               "main",
				NewestCompletedRunCreatedAt: &newest,
				LagSeconds:                  ptr(int64(finished.Sub(newest) / time.Second)),
				Runs:                        3,
				Attempts:                    4,
				BytesData:                   1234,
				Pending:                     []status.Pending{},
				RetentionDays:               90,
				DiskCapBytes:                50e9,
			}},
		}))
	})

	It("records a daemon's pid, version, next sync, served request and config error", func() {
		c := good(started)
		c.Daemon = &status.Daemon{PID: 4242, Version: "v1.2.3"}
		c.NextSyncAt = started.Add(10 * time.Minute)
		c.ServedRequest = 7
		c.ConfigError = errors.New("config.yaml:\n unknown key colour")

		st := status.Next(nil, c)

		Expect(st.DaemonPID).To(Equal(ptr(4242)))
		Expect(st.DaemonVersion).To(Equal(ptr("v1.2.3")))
		Expect(st.NextSyncAt).To(Equal(ptr(started.Add(10 * time.Minute).UTC())))
		Expect(st.ServedRequest).To(Equal(int64(7)))
		Expect(st.ConfigError).To(Equal(ptr("config.yaml: unknown key colour")))
	})

	It("keeps the served request of a daemon cycle after a one-shot sync", func() {
		daemonCycle := good(started)
		daemonCycle.ServedRequest = 7
		prev := status.Next(nil, daemonCycle)

		Expect(status.Next(&prev, good(started.Add(time.Hour))).ServedRequest).To(Equal(int64(7)))
	})

	It("keeps the served request after a cycle that was cancelled, since it served none", func() {
		prev := status.Next(nil, good(started))
		c := good(started.Add(time.Hour))
		c.Completed, c.Err, c.ServedRequest = false, fmt.Errorf("sync: %w", context.Canceled), 7

		Expect(status.Next(&prev, c).ServedRequest).To(BeZero())
	})

	It("records no next sync for a cycle that was cancelled, since no daemon will run it", func() {
		c := good(started)
		c.Completed, c.Err, c.NextSyncAt = false, fmt.Errorf("sync: %w", context.Canceled), started.Add(10*time.Minute)

		Expect(status.Next(nil, c).NextSyncAt).To(BeNil())
	})

	It("records the horizon, and no newest run or lag without a completed run on disk", func() {
		c := good(started)
		c.Disk = status.Disk{Horizon: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}

		r := status.Next(nil, c).Repos[repo]
		Expect(r.Horizon).To(Equal(ptr(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))))
		Expect(r.NewestCompletedRunCreatedAt).To(BeNil())
		Expect(r.LagSeconds).To(BeNil())
	})

	DescribeTable("rounds retention up to whole days",
		func(retention time.Duration, days int64) {
			c := good(started)
			c.Retention = retention
			Expect(status.Next(nil, c).Repos[repo].RetentionDays).To(Equal(days))
		},
		Entry("under a day", 12*time.Hour, int64(1)),
		Entry("past a day", 36*time.Hour, int64(2)),
		Entry("on a day", 48*time.Hour, int64(2)),
	)

	It("counts cycles on from the previous status", func() {
		Expect(status.Next(&status.Status{Cycle: 41}, good(started)).Cycle).To(Equal(int64(42)))
	})

	It("gives pending units with their last error, on one line without terminal controls", func() {
		c := good(started)
		c.Err = errors.New("pending units and a discarded hint file")
		c.Pending = []status.Pending{
			{Unit: status.Unit{Run: 1, Attempt: 2}, Error: "502 Bad Gateway"},
			{Unit: status.Unit{Run: 1, Artifact: 7}, Error: "digest\x1b]0;x\amismatch"},
			{Unit: status.Unit{Run: 3}, Error: errors.Join(errors.New("a"), errors.New("b")).Error()},
		}

		r := status.Next(nil, c).Repos[repo]
		Expect(r.Pending).To(Equal([]status.Pending{
			{Unit: status.Unit{Run: 1, Attempt: 2}, Error: "502 Bad Gateway"},
			{Unit: status.Unit{Run: 1, Artifact: 7}, Error: "digest ]0;x mismatch"},
			{Unit: status.Unit{Run: 3}, Error: "a b"},
		}))
		Expect(r.PendingUnits).To(Equal(3))
	})

	It("keeps, after a cycle that stopped early, the units it left and the earlier ones still missing on disk", func() {
		prev := status.Next(nil, good(started.Add(-time.Hour)))
		prev.Repos[repo] = status.Repo{Pending: []status.Pending{
			{Unit: status.Unit{Run: 1, Attempt: 1}, Error: "502"},
			{Unit: status.Unit{Run: 2, Artifact: 7}, Error: "digest mismatch"},
			{Unit: status.Unit{Run: 3}, Error: "500"},
		}}
		c := blockedCycle(started, failure.Blocked{Kind: failure.Auth, Detail: "401"})
		c.Pending = []status.Pending{{Unit: status.Unit{Run: 2, Artifact: 7}, Error: "timeout"}}
		c.Disk.Published = map[status.Unit]bool{{Run: 1, Attempt: 1}: true}

		r := status.Next(&prev, c).Repos[repo]
		Expect(r.Pending).To(Equal([]status.Pending{
			{Unit: status.Unit{Run: 2, Artifact: 7}, Error: "timeout"},
			{Unit: status.Unit{Run: 3}, Error: "500"},
		}))
		Expect(r.PendingUnits).To(Equal(2))
	})

	It("records a blocked cycle since it started, keeping the last good sync, pending units and default branch", func() {
		prev := status.Next(nil, good(started.Add(-time.Hour)))
		prev.Repos[repo] = status.Repo{DefaultBranch: "main", Pending: []status.Pending{{Unit: status.Unit{Run: 1}, Error: "502"}}, PendingUnits: 1}
		retryAt := time.Date(2026, 10, 4, 1, 5, 0, 0, time.UTC)

		st := status.Next(&prev, blockedCycle(started, failure.Blocked{Kind: failure.RateLimit, Detail: "429", RetryAt: retryAt.In(time.Local)}))

		Expect(st.Blocked).To(Equal(&status.Blocked{Since: started.UTC(), Kind: failure.RateLimit, Detail: "429", RetryAt: &retryAt}))
		Expect(st.LastSyncOKAt).To(Equal(prev.LastSyncOKAt))
		Expect(st.LastSyncFinishedAt).To(Equal(finished))
		Expect(st.Repos[repo].Pending).To(Equal([]status.Pending{{Unit: status.Unit{Run: 1}, Error: "502"}}))
		Expect(st.Repos[repo].PendingUnits).To(Equal(1))
		Expect(st.Repos[repo].DefaultBranch).To(Equal("main"))
		Expect(st.Repos[repo].Runs).To(Equal(3))
	})

	It("keeps blocked.since across blocked cycles, and clears blocked after a good cycle", func() {
		first := status.Next(nil, blockedCycle(started, failure.Blocked{Kind: failure.Auth, Detail: "401"}))
		second := status.Next(&first, blockedCycle(started.Add(time.Hour), failure.Blocked{Kind: failure.Unreachable, Detail: "dial"}))
		third := status.Next(&second, good(started.Add(2*time.Hour)))

		Expect(first.Blocked).To(Equal(&status.Blocked{Since: started.UTC(), Kind: failure.Auth, Detail: "401"}))
		Expect(first.Repos[repo].Pending).To(Equal([]status.Pending{}), "pending is [] in JSON, not null")
		Expect(second.Blocked).To(Equal(&status.Blocked{Since: started.UTC(), Kind: failure.Unreachable, Detail: "dial"}))
		Expect(third.Blocked).To(BeNil())
		Expect(third.LastSyncOKAt).To(Equal(ptr(finished.Add(2 * time.Hour))))
	})

	It("clears blocked after a cycle that failed without blocking, since its cause no longer applies", func() {
		prev := status.Next(nil, blockedCycle(started, failure.Blocked{Kind: failure.Unreachable, Detail: "dial"}))
		c := good(started.Add(time.Hour))
		c.Completed, c.Err = false, errors.New("GET /repos/rosenhouse/lg/actions/runs: 500 Internal Server Error")

		Expect(status.Next(&prev, c).Blocked).To(BeNil())
	})

	It("keeps blocked, and its since, after a cycle that was cancelled", func() {
		prev := status.Next(nil, blockedCycle(started, failure.Blocked{Kind: failure.Auth, Detail: "keyring"}))
		c := good(started.Add(time.Hour))
		c.Completed, c.Err = false, context.Canceled

		Expect(status.Next(&prev, c).Blocked).To(Equal(prev.Blocked))
	})

	It("records a cycle that stopped without blocking as neither good nor blocked", func() {
		prev := status.Next(nil, good(started.Add(-time.Hour)))
		c := good(started)
		c.Completed, c.Err = false, errors.New("GET /repos/rosenhouse/lg: full_name is missing")

		st := status.Next(&prev, c)
		Expect(st.Blocked).To(BeNil())
		Expect(st.LastSyncOKAt).To(Equal(prev.LastSyncOKAt))
		Expect(st.LastSyncFinishedAt).To(Equal(finished))
	})
})

var _ = DescribeTable("Pending names its unit before its error", Label("status"),
	func(unit status.Unit, want string) {
		Expect(status.Pending{Unit: unit, Error: "502"}.String()).To(Equal(want))
	},
	Entry("an attempt", status.Unit{Run: 1, Attempt: 2}, "run 1 attempt 2: 502"),
	Entry("an artifact", status.Unit{Run: 1, Artifact: 7}, "run 1 artifact 7: 502"),
	Entry("a run", status.Unit{Run: 1}, "run 1: 502"),
)

var _ = Describe("Remeasured", Label("status"), func() {
	It("replaces repo's disk fields, with lag as at the last sync, and keeps the rest", func() {
		prev := status.Next(nil, good(time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)))
		prev.Repos[repo] = func(r status.Repo) status.Repo {
			r.Pending = []status.Pending{{Unit: status.Unit{Run: 9}, Error: "502"}}
			r.PendingUnits = 1
			return r
		}(prev.Repos[repo])
		prev.Repos["github.com/o/other"] = status.Repo{Runs: 5}
		before := prev.Repos[repo]
		later := newest.Add(time.Hour)
		horizon := newest.Add(-time.Hour)

		st := status.Remeasured(prev, repo, status.Disk{Runs: 1, Attempts: 2, Bytes: 10, NewestCompleted: later, Horizon: horizon}, 30*24*time.Hour, 1e6)

		want := before
		want.Runs, want.Attempts, want.BytesData = 1, 2, 10
		want.NewestCompletedRunCreatedAt = &later
		want.LagSeconds = ptr(int64(prev.LastSyncFinishedAt.Sub(later) / time.Second))
		want.Horizon = &horizon
		want.RetentionDays, want.DiskCapBytes = 30, 1e6
		Expect(st.Repos).To(Equal(map[string]status.Repo{repo: want, "github.com/o/other": {Runs: 5}}))
		Expect(prev.Repos[repo]).To(Equal(before))
		st.Repos = prev.Repos
		Expect(st).To(Equal(prev))
	})

	It("clears the newest run and lag when no completed run is left", func() {
		prev := status.Next(nil, good(time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)))

		r := status.Remeasured(prev, repo, status.Disk{}, 24*time.Hour, 1).Repos[repo]
		Expect(r.NewestCompletedRunCreatedAt).To(BeNil())
		Expect(r.LagSeconds).To(BeNil())
		Expect(r.Horizon).To(BeNil())
	})
})
