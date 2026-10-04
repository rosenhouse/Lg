package mirror_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

const day = 24 * time.Hour

// cloneAt is the fixture run at stage, as run id created at the given time.
func cloneAt(id int64, stage string, at time.Time) scenario.Run {
	return scenario.CreatedAt(scenario.Clone(scenario.Recorded(runID, stage), id), at)
}

// runListings gives the query of each request that listed runs.
func runListings(requests []fakegithub.Request) []url.Values {
	GinkgoHelper()
	var queries []url.Values
	for _, r := range requests {
		if strings.HasSuffix(r.Path, "/actions/runs") {
			q, err := url.ParseQuery(r.Query)
			Expect(err).NotTo(HaveOccurred())
			queries = append(queries, q)
		}
	}
	return queries
}

// createdRanges gives the created range of each request that listed runs by one.
func createdRanges(requests []fakegithub.Request) []string {
	var ranges []string
	for _, q := range runListings(requests) {
		if q.Has("created") {
			ranges = append(ranges, q.Get("created"))
		}
	}
	return ranges
}

func createdRange(from, to time.Time) string {
	return from.UTC().Format(time.RFC3339) + ".." + to.UTC().Format(time.RFC3339)
}

// readWatch reads state/watch.json as the run ids watched per host.
func readWatch(env *harness.InProcessEnv) map[string][]string {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join(env.State(), "watch.json"))
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
		from := to.Add(-7 * day)
		for i := range int64(1001) {
			env.Fake.AddListed(scenario.ListedRun(i+1, from.Add(time.Duration(i)*10*time.Minute)))
		}

		runs, err := mirror.Discover(ctx, env.Mirror.NewGitHub(fakegh.Token), from, to)
		Expect(err).NotTo(HaveOccurred())
		ids := map[int64]bool{}
		for _, run := range runs {
			ids[run.ID] = true
		}
		Expect(ids).To(HaveLen(1001))
	}, cycleTimeout)
})

var _ = Describe("a rerun of a run created before the window", Label("discovery"), func() {
	It("is watched once listed with any non-terminal status, published by the first sync after it completes, and then no longer watched", func(ctx SpecContext) {
		env := harness.InProcess()
		old := cloneAt(1, "after-attempt-2", harness.DefaultNow().Add(-30*day))
		Expect(env.Fake.AddRun(scenario.InProgress(old, 2))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", ConsistOf("1")))
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1")))

		Expect(env.Fake.AddRun(old)).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1"), HaveSuffix("/attempt-2")))
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", BeEmpty()))
	}, cycleTimeout)
})

var _ = Describe("a watched run that GitHub deleted", Label("discovery"), func() {
	It("is dropped from the watch list", func(ctx SpecContext) {
		env := harness.InProcess()
		old := cloneAt(1, "after-attempt-2", harness.DefaultNow().Add(-30*day))
		Expect(env.Fake.AddRun(scenario.InProgress(old, 2))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", ConsistOf("1")))

		env.Fake.Remove(1)
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readWatch(env)).To(HaveKeyWithValue("github.com", BeEmpty()))
	}, cycleTimeout)
})

var _ = Describe("a watched run created before the window that completes between two polls", Label("discovery"), func() {
	It("is fetched with GET /actions/runs/{id}, because no listing returns it, and published", func(ctx SpecContext) {
		env := harness.InProcess()
		old := cloneAt(1, "after-attempt-2", harness.DefaultNow().Add(-30*day))
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
		created := now.Add(-10 * day)
		env.Clock.Set(now.Add(-9 * day))
		Expect(env.Fake.AddRun(cloneAt(1, "after-attempt-1", created))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1")))

		env.Clock.Set(now)
		Expect(env.Fake.AddRun(cloneAt(1, "after-attempt-2", created))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.AttemptDirs(1)).To(ConsistOf(HaveSuffix("/attempt-1"), HaveSuffix("/attempt-2")))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle's hourly rescan", Label("discovery"), func() {
	It("lists runs created in [now−min(30d, retention), now−backfill] with the same halving, at most once an hour", func(ctx SpecContext) {
		env := harness.InProcess()
		now := harness.DefaultNow()
		from, to := now.Add(-30*day), now.Add(-7*day)
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
		Expect(createdRanges(env.Fake.Requests()[before:])).To(ConsistOf(createdRange(now.Add(30*time.Minute-7*day), now.Add(30*time.Minute))))

		before = len(env.Fake.Requests())
		env.Clock.Set(now.Add(time.Hour))
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(createdRanges(env.Fake.Requests()[before:])).To(ContainElement(createdRange(from.Add(time.Hour), to.Add(time.Hour))))
	}, cycleTimeout)

	It("fetches nothing for runs that are not on disk", func(ctx SpecContext) {
		env := harness.InProcess()
		created := harness.DefaultNow().Add(-20 * day)
		Expect(env.Fake.AddRun(cloneAt(1, "after-attempt-1", created))).To(Succeed())
		Expect(env.Fake.AddRun(cloneAt(2, "after-attempt-2", created.Add(time.Hour)))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(createdRanges(env.Fake.Requests())).To(ContainElement(HavePrefix(created.Add(-10 * day).Format(time.RFC3339))))
		Expect(env.Fake.Requests()).NotTo(ContainElement(HaveField("Path", MatchRegexp(`/runs/[12](/|$)`))))
		Expect(env.AttemptDirs(1)).To(BeEmpty())
		Expect(env.AttemptDirs(2)).To(BeEmpty())
	}, cycleTimeout)
})
