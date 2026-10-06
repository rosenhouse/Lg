package index_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("index.RerunFlips", Label("flakes"), func() {
	const carried = 2
	var (
		env *harness.InProcessEnv
		ix  *index.Index
	)

	BeforeEach(func(ctx SpecContext) {
		env = harness.InProcess()
		// In attempt 1 "flaky" and "pass" fail; attempt 2 re-runs "flaky", attempt 3 "pass".
		r := scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), carried)
		r = scenario.SetJobConclusion(r, 1, r.JobIDs(1, "pass")[0], "failure")
		Expect(env.Fake.AddRun(scenario.AddRerunAttempt(scenario.AddRerunAttempt(r, "flaky"), "pass"))).To(Succeed())
		syncStages(ctx, env, "after-attempt-1", "after-attempt-2", "after-attempt-3")
		var err error
		ix, err = index.Open(ctx, dbPath(env), env.Data(), nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)
		Expect(ix.Reconcile(ctx)).To(Succeed())
	}, syncTimeout)

	flips := func(ctx context.Context, f index.Filter) []index.Flip {
		GinkgoHelper()
		flips, err := ix.RerunFlips(ctx, f)
		Expect(err).NotTo(HaveOccurred())
		return flips
	}

	It("gives the flips of the jobs and steps that ran, with the run's head SHA and the logs on disk", func(ctx SpecContext) {
		run := runDir(env.Data(), runID)
		log := func(attempt string, job string) string { return filepath.Join(run, attempt, "jobs", job, "log.txt") }
		Expect(flips(ctx, index.Filter{SHAs: []string{"1a51097"}})).To(HaveExactElements(
			index.Flip{HeadSHA: "1a51097dadb5b55978ac401b93f1ca9d8d317b02", Flip: model.Flip{
				RunID: runID, Job: "flaky",
				Outcomes:     []model.Outcome{{Attempt: 1, Conclusion: "failure"}, {Attempt: 2, Conclusion: "success"}, {Attempt: 3, Conclusion: "success"}},
				FailingSteps: []string{"Fail on first attempt only"},
				Logs:         []string{log("attempt-1", "111221289888_flaky"), log("attempt-2", "111221661475_flaky"), log("attempt-3", "111221892393_flaky")},
			}},
			HaveField("Flip.Step", "Fail on first attempt only"),
			HaveField("Flip.Job", "timeout"),
			HaveField("Flip.Step", "Time out on first attempt only"),
		))
	})

	It("leaves out carried-forward jobs", func(ctx SpecContext) {
		Expect(flips(ctx, index.Filter{})).To(ContainElement(SatisfyAll(
			HaveField("Flip.RunID", BeEquivalentTo(carried)),
			HaveField("Flip.Job", "pass"),
			HaveField("Flip.Step", ""),
			HaveField("Flip.Outcomes", []model.Outcome{{Attempt: 1, Conclusion: "failure"}, {Attempt: 3, Conclusion: "success"}}),
			HaveField("Flip.Logs", HaveLen(2)),
		)))
	})

	It("considers only the jobs the filter selects, as Paths does for UnitJob", func(ctx SpecContext) {
		Expect(flips(ctx, index.Filter{Jobs: []string{"time*"}})).To(HaveEach(HaveField("Flip.Job", "timeout")))
		Expect(flips(ctx, index.Filter{Jobs: []string{"time*"}})).To(HaveLen(2))
		Expect(flips(ctx, index.Filter{SHAs: []string{"2"}})).To(BeEmpty())
		Expect(flips(ctx, index.Filter{Since: time.Date(2026, 10, 3, 14, 25, 0, 0, time.UTC)})).To(BeEmpty())
	})
})
