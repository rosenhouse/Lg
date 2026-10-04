package mirror_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// createdRanges gives the created range of each request that listed runs by one.
func createdRanges(requests []fakegithub.Request) []string {
	var ranges []string
	for _, q := range fakegithub.RunListings(requests) {
		if q.Has("created") {
			ranges = append(ranges, q.Get("created"))
		}
	}
	return ranges
}

func createdRange(from, to time.Time) string {
	return from.UTC().Format(time.RFC3339) + ".." + to.UTC().Format(time.RFC3339)
}

// readWatch reads state/watch.json as the run ids watched per host, which
// is empty while the file is missing.
func readWatch(env *harness.InProcessEnv) map[string][]string {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join(env.State(), "watch.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string][]string{}
	}
	Expect(err).NotTo(HaveOccurred())
	var hosts map[string]map[string]json.RawMessage
	Expect(json.Unmarshal(raw, &hosts)).To(Succeed())
	watched := map[string][]string{}
	for host, runs := range hosts {
		watched[host] = []string{}
		for id := range runs {
			watched[host] = append(watched[host], id)
		}
	}
	return watched
}

var _ = Describe("mirror.Discover", Label("discovery"), func() {
	It("finds all 1,001 runs of a window by halving a range whose total_count is at least 1,000", func(ctx SpecContext) {
		env := harness.InProcess()
		to := harness.DefaultNow()
		from := to.Add(-7 * scenario.Day)
		for i := range int64(1001) {
			env.Fake.AddListed(scenario.ListedRun(i+1, from.Add(time.Duration(i)*10*time.Minute)))
		}

		runs, capped, err := mirror.Discover(ctx, env.Mirror.NewGitHub(fakegh.Token), from, to)
		Expect(err).NotTo(HaveOccurred())
		Expect(capped).NotTo(HaveOccurred())
		ids := map[int64]bool{}
		for _, run := range runs {
			ids[run.ID] = true
		}
		Expect(ids).To(HaveLen(1001))
	}, cycleTimeout)

	It("lists a range once when GitHub serves a complete listing whose total_count reaches 1,000", func(ctx SpecContext) {
		env := harness.InProcess()
		to := harness.DefaultNow()
		body := fmt.Sprintf(`{"total_count":%d,"workflow_runs":[%s]}`, github.ListingCap, scenario.ListedRun(1, to.Add(-scenario.Day)))
		env.Fake.Fail("api", "/actions/runs", fakegithub.Fault{Status: http.StatusOK, Body: body})

		runs, capped, err := mirror.Discover(ctx, env.Mirror.NewGitHub(fakegh.Token), to.Add(-7*scenario.Day), to)
		Expect(err).NotTo(HaveOccurred())
		Expect(capped).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1))
		Expect(env.Fake.Requests()).To(HaveLen(1))
	}, cycleTimeout)

	It("narrows a range with sub-second bounds down to the whole second GitHub caps", func(ctx SpecContext) {
		env := harness.InProcess()
		burst := harness.DefaultNow().Add(-scenario.Day)
		for i := range int64(1001) {
			env.Fake.AddListed(scenario.ListedRun(i+1, burst))
		}

		runs, capped, err := mirror.Discover(ctx, env.Mirror.NewGitHub(fakegh.Token), burst.Add(-900*time.Millisecond), burst.Add(time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1000))
		second := burst.Format(time.RFC3339)
		Expect(capped).To(MatchError(ContainSubstring("[" + second + ", " + second + "]")))
	}, cycleTimeout)

	It("gives the 1,000 runs GitHub lists of a second holding 1,001, and reports the rest as a run-scoped error", func(ctx SpecContext) {
		env := harness.InProcess()
		burst := harness.DefaultNow().Add(-scenario.Day)
		for i := range int64(1001) {
			env.Fake.AddListed(scenario.ListedRun(i+1, burst))
		}

		runs, capped, err := mirror.Discover(ctx, env.Mirror.NewGitHub(fakegh.Token), burst.Add(-7*scenario.Day), burst.Add(scenario.Day))
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1000))
		Expect(capped).To(MatchError(MatchRegexp(`^1001 runs were created in \[[0-9TZ:-]+, ` + burst.Format(time.RFC3339) + `\], and GitHub lists at most 1000$`)))
		Expect(mirror.RunScoped(capped)).To(BeTrue())
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when over 1,000 runs were created in one second", Label("discovery"), func() {
	It("syncs every other run, watches the 1,000 listed, and reports the rest", func(ctx SpecContext) {
		env := harness.InProcess()
		now := harness.DefaultNow()
		for i := range int64(1001) {
			env.Fake.AddListed(scenario.ListedRun(i+1, now.Add(-scenario.Day)))
		}
		Expect(env.Fake.AddRun(scenario.CloneAt(2000, "after-attempt-1", now.Add(-2*scenario.Day)))).To(Succeed())

		Expect(env.Sync(ctx)).To(MatchError(ContainSubstring("1001 runs were created in")))
		Expect(env.AttemptDirs(2000)).To(ConsistOf(HaveSuffix("/attempt-1")))
		Expect(readWatch(env)["github.com"]).To(HaveLen(1000))
	}, cycleTimeout)
})

var _ = Describe("a non-terminal status listing whose total_count reaches 1,000", Label("discovery"), func() {
	It("is listed again by created range over retention, halved the same way, so every run is watched", func(ctx SpecContext) {
		env := harness.InProcess()
		now := harness.DefaultNow()
		from := now.Add(-20 * scenario.Day)
		for i := range int64(1001) {
			env.Fake.AddListed(scenario.QueuedRun(i+1, from.Add(time.Duration(i)*time.Minute)))
		}

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readWatch(env)["github.com"]).To(HaveLen(1001))
		var queuedRanges []string
		for _, q := range fakegithub.RunListings(env.Fake.Requests()) {
			if q.Get("status") == "queued" && q.Has("created") {
				queuedRanges = append(queuedRanges, q.Get("created"))
			}
		}
		retention := createdRange(now.Add(-90*scenario.Day), now)
		Expect(queuedRanges).To(ContainElements(retention, createdRange(now.Add(-90*scenario.Day), now.Add(-45*scenario.Day)), createdRange(now.Add(-45*scenario.Day).Add(time.Second), now)))
	}, cycleTimeout)
})

var _ = Describe("a rerun of a run created before the window", Label("discovery"), func() {
	It("is watched once listed with any non-terminal status, published by the first sync after it completes, and then no longer watched", func(ctx SpecContext) {
		env := harness.InProcess()
		old := scenario.CloneAt(1, "after-attempt-2", harness.DefaultNow().Add(-30*scenario.Day))
		Expect(env.Fake.AddRun(scenario.InProgress(old, 2))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", ConsistOf("1")))
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1")))

		before := len(env.Fake.Requests())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.Fake.Requests()[before:]).NotTo(ContainElement(HaveField("Path", "/repos/rosenhouse/lg/actions/runs/1")), "a run still listed is not fetched")
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", ConsistOf("1")))

		Expect(env.Fake.AddRun(old)).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1"), HaveSuffix("/attempt-2")))
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", BeEmpty()))
	}, cycleTimeout)

	It("stays watched while its completed attempt fails to download", func(ctx SpecContext) {
		env := harness.InProcess()
		old := scenario.CloneAt(1, "after-attempt-2", harness.DefaultNow().Add(-30*scenario.Day))
		Expect(env.Fake.AddRun(scenario.InProgress(old, 2))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.Fake.AddRun(old)).To(Succeed())
		env.Fake.Fail("api", "/actions/runs/1/attempts/2", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})

		Expect(env.Sync(ctx)).To(MatchError(ContainSubstring("run 1 attempt 2")))
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1")))
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", ConsistOf("1")))

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1"), HaveSuffix("/attempt-2")))
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", BeEmpty()))
	}, cycleTimeout)
})

var _ = Describe("a watched run that GitHub deleted", Label("discovery"), func() {
	It("is dropped from the watch list", func(ctx SpecContext) {
		env := harness.InProcess()
		old := scenario.CloneAt(1, "after-attempt-2", harness.DefaultNow().Add(-30*scenario.Day))
		Expect(env.Fake.AddRun(scenario.InProgress(old, 2))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", ConsistOf("1")))

		env.Fake.Remove(1)
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", BeEmpty()))
	}, cycleTimeout)
})

var _ = Describe("a watched run whose attempt GitHub deleted", Label("discovery"), func() {
	It("is dropped from the watch list", func(ctx SpecContext) {
		env := harness.InProcess()
		old := scenario.CloneAt(1, "after-attempt-2", harness.DefaultNow().Add(-30*scenario.Day))
		Expect(env.Fake.AddRun(scenario.InProgress(old, 2))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.Fake.AddRun(old)).To(Succeed())
		env.Fake.Fail("api", "/actions/runs/1/attempts/2", fakegithub.Fault{Status: http.StatusNotFound, Times: 1})

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1")))
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", BeEmpty()))
	}, cycleTimeout)
})

var _ = Describe("a watched run created before the window that completes between two polls", Label("discovery"), func() {
	It("is fetched with GET /actions/runs/{id}, because no listing returns it, and published", func(ctx SpecContext) {
		env := harness.InProcess()
		old := scenario.CloneAt(1, "after-attempt-2", harness.DefaultNow().Add(-30*scenario.Day))
		Expect(env.Fake.AddRun(scenario.InProgress(old, 2))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.Fake.AddRun(old)).To(Succeed())
		before := len(env.Fake.Requests())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.Fake.Requests()[before:]).To(ContainElement(HaveField("Path", "/repos/rosenhouse/lg/actions/runs/1")))
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1"), HaveSuffix("/attempt-2")))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle for a run created 10 days ago, with attempt-1 on disk, that gains a completed attempt 2 without ever being listed in a non-terminal status", Label("discovery"), func() {
	It("gets attempt-2 from the hourly rescan", func(ctx SpecContext) {
		env := harness.InProcess()
		now := harness.DefaultNow()
		created := now.Add(-10 * scenario.Day)
		env.Clock.Set(now.Add(-9 * scenario.Day))
		Expect(env.Fake.AddRun(scenario.CloneAt(1, "after-attempt-1", created))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1")))

		env.Clock.Set(now)
		Expect(env.Fake.AddRun(scenario.CloneAt(1, "after-attempt-2", created))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1"), HaveSuffix("/attempt-2")))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle's hourly rescan", Label("discovery"), func() {
	It("lists runs created in [now−min(30d, retention), now−backfill] with the same halving, at most once an hour", func(ctx SpecContext) {
		env := harness.InProcess()
		now := harness.DefaultNow()
		from, to := now.Add(-30*scenario.Day), now.Add(-7*scenario.Day)
		for i := range int64(1001) {
			env.Fake.AddListed(scenario.ListedRun(i+1, from.Add(time.Duration(i)*30*time.Minute)))
		}

		Expect(env.Sync(ctx)).To(Succeed())
		mid := from.Add(to.Sub(from) / 2)
		Expect(createdRanges(env.Fake.Requests())).To(ContainElements(
			createdRange(from, to), createdRange(from, mid), createdRange(mid.Add(time.Second), to)))

		before := len(env.Fake.Requests())
		env.Clock.Set(now.Add(30 * time.Minute))
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(createdRanges(env.Fake.Requests()[before:])).To(ConsistOf(createdRange(now.Add(30*time.Minute-7*scenario.Day), now.Add(30*time.Minute))))

		before = len(env.Fake.Requests())
		env.Clock.Set(now.Add(time.Hour))
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(createdRanges(env.Fake.Requests()[before:])).To(ContainElement(createdRange(from.Add(time.Hour), to.Add(time.Hour))))
	}, cycleTimeout)

	It("fetches nothing for runs that are not on disk", func(ctx SpecContext) {
		env := harness.InProcess()
		created := harness.DefaultNow().Add(-20 * scenario.Day)
		Expect(env.Fake.AddRun(scenario.CloneAt(1, "after-attempt-1", created))).To(Succeed())
		Expect(env.Fake.AddRun(scenario.CloneAt(2, "after-attempt-2", created.Add(time.Hour)))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(createdRanges(env.Fake.Requests())).To(ContainElement(HavePrefix(created.Add(-10 * scenario.Day).Format(time.RFC3339))))
		Expect(env.Fake.Requests()).NotTo(ContainElement(HaveField("Path", MatchRegexp(`/runs/[12](/|$)`))))
		Expect(env.AttemptDirs(1)).To(BeEmpty())
		Expect(env.AttemptDirs(2)).To(BeEmpty())
	}, cycleTimeout)
})

func listedAt(id int64, at time.Time) github.Run {
	return github.Run{Run: model.Run{ID: id, CreatedAt: at}}
}

var _ = Describe("Merge", Label("discovery"), func() {
	now := harness.DefaultNow()

	It("sorts runs by created_at, then id", func() {
		Expect(mirror.Merge([]github.Run{listedAt(3, now), listedAt(2, now), listedAt(1, now.Add(-scenario.Day))})).To(Equal(
			[]github.Run{listedAt(1, now.Add(-scenario.Day)), listedAt(2, now), listedAt(3, now)}))
	})

	It("collapses duplicates across listings, keeping the first", func() {
		first := github.Run{Run: model.Run{ID: 1, CreatedAt: now, Status: "completed"}}
		again := github.Run{Run: model.Run{ID: 1, CreatedAt: now, Status: "in_progress"}}
		Expect(mirror.Merge([]github.Run{first}, []github.Run{again, listedAt(2, now)})).To(Equal([]github.Run{first, listedAt(2, now)}))
	})
})

var _ = Describe("RescanWindow", Label("discovery"), func() {
	now := harness.DefaultNow()

	DescribeTable("is [now−min(30d, retention), now−backfill]",
		func(backfill, retention time.Duration, from, to time.Time) {
			gotFrom, gotTo, ok := mirror.RescanWindow(now, backfill, retention)
			Expect(ok).To(BeTrue())
			Expect(gotFrom).To(Equal(from))
			Expect(gotTo).To(Equal(to))
		},
		Entry("retention over 30d", 7*scenario.Day, 90*scenario.Day, now.Add(-30*scenario.Day), now.Add(-7*scenario.Day)),
		Entry("retention under 30d", 7*scenario.Day, 20*scenario.Day, now.Add(-20*scenario.Day), now.Add(-7*scenario.Day)),
		Entry("backfill just under the lower bound", 29*scenario.Day, 90*scenario.Day, now.Add(-30*scenario.Day), now.Add(-29*scenario.Day)),
	)

	DescribeTable("is empty when backfill is at least min(30d, retention)",
		func(backfill, retention time.Duration) {
			_, _, ok := mirror.RescanWindow(now, backfill, retention)
			Expect(ok).To(BeFalse())
		},
		Entry("backfill 30d", 30*scenario.Day, 90*scenario.Day),
		Entry("backfill over 30d", 40*scenario.Day, 90*scenario.Day),
		Entry("backfill equal to a short retention", 10*scenario.Day, 10*scenario.Day),
	)
})

var _ = Describe("mirror.Cycle", Label("discovery"), func() {
	It("lists the non-terminal statuses in_progress, queued, requested, waiting, pending and action_required", func(ctx SpecContext) {
		env := harness.InProcess()

		Expect(env.Sync(ctx)).To(Succeed())
		var statuses []string
		for _, q := range fakegithub.RunListings(env.Fake.Requests()) {
			if q.Has("status") {
				statuses = append(statuses, q.Get("status"))
			}
		}
		Expect(statuses).To(ConsistOf("in_progress", "queued", "requested", "waiting", "pending", "action_required"))
	}, cycleTimeout)

	It("rescans when state/rescan.json is missing or records a rescan at least 1h old", func(ctx SpecContext) {
		env := harness.InProcess()
		now := harness.DefaultNow()
		rescanRange := createdRange(now.Add(-30*scenario.Day), now.Add(-7*scenario.Day))
		rescanJSON := filepath.Join(env.State(), "rescan.json")

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(createdRanges(env.Fake.Requests())).To(ContainElement(rescanRange))
		Expect(readJSONFile(rescanJSON)).To(HaveKeyWithValue("rescanned_at", now.Format(time.RFC3339)))

		for _, age := range []time.Duration{0, 59 * time.Minute} {
			Expect(os.WriteFile(rescanJSON, fmt.Appendf(nil, `{"rescanned_at":%q}`, now.Add(-age).Format(time.RFC3339)), 0o644)).To(Succeed())
			before := len(env.Fake.Requests())
			Expect(env.Sync(ctx)).To(Succeed())
			Expect(createdRanges(env.Fake.Requests()[before:])).NotTo(ContainElement(rescanRange), "age %s", age)
		}
		Expect(os.WriteFile(rescanJSON, fmt.Appendf(nil, `{"rescanned_at":%q}`, now.Add(-time.Hour).Format(time.RFC3339)), 0o644)).To(Succeed())
		before := len(env.Fake.Requests())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(createdRanges(env.Fake.Requests()[before:])).To(ContainElement(rescanRange))
	}, cycleTimeout)

	It("rescans when state/rescan.json records a time after now", func(ctx SpecContext) {
		env := harness.InProcess()
		now := harness.DefaultNow()
		rescanJSON := filepath.Join(env.State(), "rescan.json")
		Expect(os.MkdirAll(env.State(), 0o755)).To(Succeed())
		Expect(os.WriteFile(rescanJSON, fmt.Appendf(nil, `{"rescanned_at":%q}`, now.Add(time.Minute).Format(time.RFC3339)), 0o644)).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(createdRanges(env.Fake.Requests())).To(ContainElement(createdRange(now.Add(-30*scenario.Day), now.Add(-7*scenario.Day))))
		Expect(readJSONFile(rescanJSON)).To(HaveKeyWithValue("rescanned_at", now.Format(time.RFC3339)))
	}, cycleTimeout)
})

var _ = Describe("the watch list", Label("discovery"), func() {
	It("holds each watched run's created_at under its id", func(ctx SpecContext) {
		env := harness.InProcess()
		created := harness.DefaultNow().Add(-30 * scenario.Day)
		Expect(env.Fake.AddRun(scenario.InProgress(scenario.CloneAt(1, "after-attempt-2", created), 2))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		raw, err := os.ReadFile(filepath.Join(env.State(), "watch.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).To(MatchJSON(fmt.Sprintf(`{"github.com":{"1":{"created_at":%q}}}`, created.Format(time.RFC3339))))
	}, cycleTimeout)

	It("drops a run that is complete on disk after one GET /actions/runs/{id}, and a run older than retention without fetching it", func(ctx SpecContext) {
		env := harness.InProcess()
		now := harness.DefaultNow()
		env.Clock.Set(now.Add(-39 * scenario.Day))
		Expect(env.Fake.AddRun(scenario.CloneAt(1, "after-attempt-1", now.Add(-40*scenario.Day)))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1")))
		env.Clock.Set(now)
		watched := fmt.Sprintf(`{"github.com":{"1":%s,"5":%s}}`,
			scenario.ListedRun(1, now.Add(-40*scenario.Day)), scenario.ListedRun(5, now.Add(-91*scenario.Day)))
		Expect(os.WriteFile(filepath.Join(env.State(), "watch.json"), []byte(watched), 0o644)).To(Succeed())
		before := len(env.Fake.Requests())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", BeEmpty()))
		requests := env.Fake.Requests()[before:]
		Expect(requests).To(ContainElement(HaveField("Path", "/repos/rosenhouse/lg/actions/runs/1")))
		Expect(requests).NotTo(ContainElement(HaveField("Path", MatchRegexp(`/runs/1/`))))
		Expect(requests).NotTo(ContainElement(HaveField("Path", MatchRegexp(`/runs/5(/|$)`))))
	}, cycleTimeout)
})

var _ = Describe("a listed run without created_at", Label("discovery"), func() {
	It("is reported as malformed, and neither fetched nor watched", func(ctx SpecContext) {
		env := harness.InProcess()
		env.Fake.AddListed(json.RawMessage(`{"id":7,"status":"in_progress","conclusion":null,"run_attempt":1,"repository":{"full_name":"rosenhouse/Lg"}}`))

		err := env.Sync(ctx)
		Expect(err).To(MatchError(ContainSubstring("run 7: no created_at")))
		Expect(mirror.RunScoped(err)).To(BeTrue())
		Expect(env.Fake.Requests()).NotTo(ContainElement(HaveField("Path", MatchRegexp(`/runs/7(/|$)`))))
		Expect(readWatch(env)["github.com"]).NotTo(ContainElement("7"))
	}, cycleTimeout)
})

var _ = Describe("an in_progress run created before retention", Label("discovery"), func() {
	It("is neither fetched nor watched", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(scenario.InProgress(scenario.CloneAt(1, "after-attempt-2", harness.DefaultNow().Add(-91*scenario.Day)), 2))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.Fake.Requests()).NotTo(ContainElement(HaveField("Path", MatchRegexp(`/runs/1(/|$)`))))
		Expect(env.AttemptDirs(1)).To(BeEmpty())
		Expect(readWatch(env)["github.com"]).NotTo(ContainElement("1"))
	}, cycleTimeout)
})
