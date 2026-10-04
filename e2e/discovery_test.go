package e2e_test

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

const day = 24 * time.Hour

// cloneAt is the fixture run at after-attempt-1, as run id created at the given time.
func cloneAt(id int64, at time.Time) scenario.Run {
	return scenario.CreatedAt(scenario.Clone(scenario.Recorded(fixtureRun, "after-attempt-1"), id), at)
}

// runListings gives the query of each request that listed runs.
func runListings(fake *fakegithub.Server) []url.Values {
	GinkgoHelper()
	var queries []url.Values
	for _, r := range fake.Requests() {
		if strings.HasSuffix(r.Path, "/actions/runs") {
			q, err := url.ParseQuery(r.Query)
			Expect(err).NotTo(HaveOccurred())
			queries = append(queries, q)
		}
	}
	return queries
}

// closedRange parses created=<from>..<to>.
func closedRange(q url.Values) (from, to time.Time) {
	GinkgoHelper()
	a, b, ok := strings.Cut(q.Get("created"), "..")
	Expect(ok).To(BeTrue(), "created=%q is not a closed range", q.Get("created"))
	var err error
	from, err = time.Parse(time.RFC3339, a)
	Expect(err).NotTo(HaveOccurred())
	to, err = time.Parse(time.RFC3339, b)
	Expect(err).NotTo(HaveOccurred())
	return from, to
}

var _ = Describe("lg init --repo rosenhouse/lg", Label("discovery"), func() {
	It("writes config.yaml holding only host and repo, and initializes the store", func() {
		env := harness.New(lgPath)

		session := env.Lg("init", "--repo", "rosenhouse/lg")
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(os.ReadFile(env.ConfigFile())).To(BeEquivalentTo("host: github.com\nrepo: rosenhouse/lg\n"))
		Expect(os.ReadFile(filepath.Join(env.Store(), "FORMAT"))).To(BeEquivalentTo("lg-store 1\n"))
	})

	It("lowercases --host, as config.Load does", func() {
		env := harness.New(lgPath)

		Eventually(env.Lg("init", "--repo", "rosenhouse/lg", "--host", "GitHub.com"), harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(os.ReadFile(env.ConfigFile())).To(BeEquivalentTo("host: github.com\nrepo: rosenhouse/lg\n"))
	})
})

var _ = Describe("lg init", Label("discovery"), func() {
	It("refuses to overwrite an existing config", func() {
		env := harness.New(lgPath)
		env.WriteConfig("http://127.0.0.1:1")
		before, err := os.ReadFile(env.ConfigFile())
		Expect(err).NotTo(HaveOccurred())

		session := env.Lg("init", "--repo", "other/repo")
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(2))
		Expect(session.Err).To(gbytes.Say(regexp.QuoteMeta(env.ConfigFile()) + " already exists"))
		Expect(os.ReadFile(env.ConfigFile())).To(Equal(before))
		Expect(filepath.Join(env.Store(), "FORMAT")).NotTo(BeAnExistingFile())
	})
})

var _ = Describe("lg sync", Label("discovery"), func() {
	var (
		env  *harness.Env
		fake *fakegithub.Server
	)

	BeforeEach(func() {
		env = harness.New(lgPath)
		fake = fakegithub.New()
		DeferCleanup(fake.Close)
		env.WriteConfig(fake.URL())
	})

	DescribeTable("exits 2 naming the key for an unknown config key, a sync_interval under 1m, or a backfill longer than retention",
		func(line, message string) {
			env.WriteConfig(fake.URL(), line)

			session := env.Sync()
			Expect(session).To(gexec.Exit(2))
			Expect(string(session.Err.Contents())).To(ContainSubstring(message))
			Expect(fake.Requests()).To(BeEmpty())
		},
		Entry("an unknown key", "sync-interval: 10m", "sync-interval"),
		Entry("a sync_interval under 1m", "sync_interval: 59s", "sync_interval must be at least 1m: 59s"),
		Entry("a backfill longer than retention", "backfill: 91d", "backfill must not exceed retention: 91d > 90d"),
		Entry("a negative backfill", "backfill: -24h", "backfill must be positive: -1d"),
		Entry("a zero backfill", "backfill: 0", "backfill must be positive: 0s"),
		Entry("a zero retention", "retention: 0d", "retention must be positive: 0s"),
	)

	It("lists the backfill window as one closed created range from LG_TEST_NOW minus 7d to LG_TEST_NOW with per_page=100, and from minus 30d with backfill 30d", func() {
		for backfill, lines := range map[time.Duration][]string{7 * day: nil, 30 * day: {"backfill: 30d"}} {
			env.WriteConfig(fake.URL(), lines...)
			before := len(runListings(fake))

			Expect(env.Sync()).To(gexec.Exit(0))
			var windows []url.Values
			for _, q := range runListings(fake)[before:] {
				if q.Has("created") {
					windows = append(windows, q)
				}
			}
			Expect(windows).NotTo(BeEmpty(), "backfill %s", backfill)
			from, to := closedRange(windows[0])
			Expect(to).To(BeTemporally("==", harness.DefaultNow()), "backfill %s", backfill)
			Expect(from).To(Equal(to.Add(-backfill)), "backfill %s", backfill)
			Expect(windows[0].Get("per_page")).To(Equal("100"))
		}
	})

	It("skips a run created before the window", func() {
		Expect(fake.AddRun(cloneAt(1, harness.DefaultNow().Add(-10*day)))).To(Succeed())

		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(filepath.Glob(filepath.Join(env.Data(), "*/*/*/runs/*/1_*"))).To(BeEmpty())
		Expect(fake.Requests()).NotTo(ContainElement(HaveField("Path", MatchRegexp(`/runs/1(/|$)`))))
	})

	It("fetches older runs' attempts before newer ones", func() {
		now := harness.DefaultNow()
		Expect(fake.AddRun(cloneAt(1, now.Add(-3*day)))).To(Succeed())
		Expect(fake.AddRun(cloneAt(2, now.Add(-day)))).To(Succeed())
		Expect(fake.AddRun(cloneAt(3, now.Add(-2*day)))).To(Succeed())

		Expect(env.Sync()).To(gexec.Exit(0))
		attempt := regexp.MustCompile(`/runs/(\d+)/attempts/1$`)
		var order []string
		for _, r := range fake.Requests() {
			if m := attempt.FindStringSubmatch(r.Path); m != nil {
				order = append(order, m[1])
			}
		}
		Expect(order).To(Equal([]string{"1", "3", "2"}))
	})

	It("makes only discovery requests when every listed run is complete and on disk", func() {
		Expect(fake.Load(fixtureRun, "after-attempt-1")).To(Succeed())
		Expect(env.Sync()).To(gexec.Exit(0))
		before := len(fake.Requests())

		Expect(env.Sync()).To(gexec.Exit(0))
		Expect(fake.Requests()[before:]).NotTo(BeEmpty())
		Expect(fake.Requests()[before:]).To(HaveEach(HaveField("Path", BeElementOf(
			"/repos/rosenhouse/lg",
			"/repos/rosenhouse/lg/actions/runs",
		))))
	})
})

var _ = Describe("lg sync with runs served 2 per page", Label("discovery"), func() {
	It("mirrors all 5 scenario runs", func() {
		env := harness.New(lgPath)
		fake := fakegithub.New()
		DeferCleanup(fake.Close)
		env.WriteConfig(fake.URL())
		fake.SetPageCap(2)
		for id := range int64(5) {
			Expect(fake.AddRun(cloneAt(id+1, harness.DefaultNow().Add(-time.Duration(id+1)*day)))).To(Succeed())
		}

		Expect(env.Sync()).To(gexec.Exit(0))
		for id := range int64(5) {
			Expect(filepath.Glob(filepath.Join(env.Data(), "*/*/*/runs/*", fmt.Sprintf("%d_*", id+1), "attempt-1"))).To(HaveLen(1), "run %d", id+1)
		}
	})
})
