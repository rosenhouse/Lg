package mirror_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/retention"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("discovery with an eviction horizon", Label("retention"), func() {
	var (
		env     *harness.InProcessEnv
		horizon time.Time
	)

	BeforeEach(func() {
		env = harness.InProcess()
		horizon = harness.DefaultNow().Add(-2 * scenario.Day)
	})

	runRequest := func(id int64) types.GomegaMatcher {
		return HaveField("Path", MatchRegexp(fmt.Sprintf(`/runs/%d(/|$)`, id)))
	}

	It("skips listed runs created at or before the horizon", func(ctx SpecContext) {
		Expect(env.Fake.AddRun(scenario.CloneAt(1, "after-attempt-1", horizon))).To(Succeed())
		Expect(env.Fake.AddRun(scenario.CloneAt(2, "after-attempt-1", horizon.Add(-time.Hour)))).To(Succeed())
		Expect(env.Fake.AddRun(scenario.CloneAt(3, "after-attempt-1", horizon.Add(time.Second)))).To(Succeed())
		Expect(retention.Horizons{"github.com/rosenhouse/Lg": horizon}.Write(env.Mirror.Store)).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.Fake.Requests()).NotTo(ContainElement(runRequest(1)))
		Expect(env.Fake.Requests()).NotTo(ContainElement(runRequest(2)))
		Expect(env.AttemptDirs(3)).To(HaveLen(1))
	}, cycleTimeout)

	It("fetches no watched run, and no pending artifact of a run, created at or before the horizon", func(ctx SpecContext) {
		const pendingRun = 6
		Expect(env.Fake.AddRun(scenario.CloneAt(pendingRun, "after-attempt-1", horizon.Add(-time.Hour)))).To(Succeed())
		zip := fmt.Sprintf("artifacts/%d/zip", pendingRun*1_000_000_000_000+11276401837)
		env.Fake.Fail("api", zip, fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		Expect(env.Sync(ctx)).To(BeTransient())
		env.Fake.Remove(pendingRun)
		watched := fmt.Sprintf(`{"github.com":{"4":%s}}`, scenario.ListedRun(4, horizon.Add(-10*scenario.Day)))
		Expect(os.WriteFile(filepath.Join(env.State(), "watch.json"), []byte(watched), 0o644)).To(Succeed())
		Expect(retention.Horizons{"github.com/rosenhouse/Lg": horizon}.Write(env.Mirror.Store)).To(Succeed())
		before := len(env.Fake.Requests())

		Expect(env.Mirror.Cycle(ctx)).To(Succeed())
		Expect(env.Fake.Requests()[before:]).NotTo(ContainElement(runRequest(4)))
		Expect(env.Fake.Requests()[before:]).NotTo(ContainElement(runRequest(pendingRun)))
		Expect(os.ReadFile(filepath.Join(env.State(), "pending-artifacts.json"))).To(MatchJSON(`{"github.com":{}}`))
		Expect(env.ArtifactDirs(pendingRun)).To(BeEmpty(), "retention evicts the run the horizon passes")
	}, cycleTimeout)
})

var _ = Describe("discovery after disk_cap evicts under another repo dir", Label("retention"), func() {
	It("fetches this repo dir's runs created at or before the other's horizon", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Mirror.DiskCap = 1
		Expect(env.Mirror.Cycle(ctx)).To(Succeed())
		Expect(env.AttemptDirs(runID)).To(BeEmpty())

		env.Mirror.Host = "ghe.example.com"
		env.Mirror.DiskCap = int64(config.Defaults().DiskCap)
		Expect(env.Mirror.Cycle(ctx)).To(Succeed())
		Expect(env.AttemptDirs(runID)).To(ConsistOf(HavePrefix(filepath.Join(env.Data(), "ghe.example.com") + "/")))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when state/horizon.json does not parse", Label("retention"), func() {
	It("moves it aside, reports it, and syncs as if it were missing", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		horizon := filepath.Join(env.State(), "horizon.json")
		Expect(os.WriteFile(horizon, []byte("{"), 0o644)).To(Succeed())

		err := env.Sync(ctx)
		Expect(err).To(MatchError(ContainSubstring(horizon)))
		Expect(mirror.RunScoped(err)).To(BeTrue())
		Expect(env.AttemptDirs(runID)).To(HaveLen(1))
		Expect(os.ReadFile(horizon + ".corrupt")).To(Equal([]byte("{")))

		Expect(env.Sync(ctx)).To(Succeed())
	}, cycleTimeout)
})
