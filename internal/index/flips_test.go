package index_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("index.RerunFlips", Label("flakes"), func() {
	const (
		carried   = 2
		twoSteps  = 3
		noSteps   = 4
		stepEmit  = "Emit log markers"
		stepBuild = "Build nested archives"
	)
	var (
		env *harness.InProcessEnv
		ix  *index.Index
	)

	BeforeEach(func(ctx SpecContext) {
		env = harness.InProcess()
		// In attempt 1 "flaky" and "pass" fail; attempt 2 re-runs "flaky", attempt 3 "pass".
		// GitHub has deleted the log of attempt 1's "flaky".
		r := scenario.WithSHA(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), carried), strings.Repeat("2", 40))
		r = scenario.SetJobConclusion(r, 1, r.JobIDs(1, "pass")[0], "failure")
		env.Fake.Fail("api", fmt.Sprintf("jobs/%d/logs", r.JobIDs(1, "flaky")[0]), fakegithub.Fault{Status: http.StatusNotFound})
		Expect(env.Fake.AddRun(scenario.AddRerunAttempt(scenario.AddRerunAttempt(r, "flaky"), "pass"))).To(Succeed())
		// In attempt 1 two steps of "pass", listed out of number order, fail; attempt 2 re-runs it.
		r = scenario.WithSHA(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), twoSteps), strings.Repeat("3", 40))
		pass := r.JobIDs(1, "pass")[0]
		r = scenario.ReverseSteps(r, 1, pass)
		r = scenario.SetStepConclusion(r, 1, pass, stepBuild, "failure")
		r = scenario.SetStepConclusion(r, 1, pass, stepEmit, "failure")
		r = scenario.SetJobConclusion(r, 1, pass, "failure")
		Expect(env.Fake.AddRun(scenario.AddRerunAttempt(r, "pass"))).To(Succeed())
		// In attempt 1 "pass" ran no step and failed; attempt 2 re-runs it.
		r = scenario.WithSHA(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), noSteps), strings.Repeat("4", 40))
		pass = r.JobIDs(1, "pass")[0]
		r = scenario.SetJobConclusion(scenario.ClearSteps(r, 1, pass), 1, pass, "failure")
		Expect(env.Fake.AddRun(scenario.AddRerunAttempt(r, "pass"))).To(Succeed())
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

	jobFlip := func(run int64, job string, outcomes ...model.Outcome) types.GomegaMatcher {
		return SatisfyAll(HaveField("Flip.RunID", BeEquivalentTo(run)), HaveField("Flip.Job", job), HaveField("Flip.Step", ""), HaveField("Flip.Outcomes", outcomes))
	}
	flaky := func() types.GomegaMatcher {
		return jobFlip(runID, "flaky", model.Outcome{Attempt: 1, Conclusion: "failure"}, model.Outcome{Attempt: 2, Conclusion: "success"}, model.Outcome{Attempt: 3, Conclusion: "success"})
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
			jobFlip(carried, "pass", model.Outcome{Attempt: 1, Conclusion: "failure"}, model.Outcome{Attempt: 3, Conclusion: "success"}),
			HaveField("Flip.Logs", HaveLen(2)),
		)))
	})

	It("gives the flip of a job that ran no step", func(ctx SpecContext) {
		Expect(flips(ctx, index.Filter{SHAs: []string{"4"}})).To(HaveExactElements(
			jobFlip(noSteps, "pass", model.Outcome{Attempt: 1, Conclusion: "failure"}, model.Outcome{Attempt: 2, Conclusion: "success"}),
		))
	})

	It("lists only logs that are regular files", func(ctx SpecContext) {
		removed := filepath.Join(runDir(env.Data(), runID), "attempt-2", "jobs", "111221661475_flaky", "log.txt")
		Expect(os.Remove(removed)).To(Succeed())

		Expect(flips(ctx, index.Filter{Jobs: []string{"flaky"}})).To(ContainElements(
			SatisfyAll(flaky(), HaveField("Flip.Logs", SatisfyAll(HaveLen(2), HaveEach(BeARegularFile())))),
			SatisfyAll(HaveField("Flip.RunID", BeEquivalentTo(carried)), HaveField("Flip.Step", ""),
				HaveField("Flip.Logs", HaveExactElements(SatisfyAll(ContainSubstring("attempt-2"), BeARegularFile())))),
		))
	})

	spoil := func(dir string) {
		GinkgoHelper()
		Expect(os.RemoveAll(dir)).To(Succeed())
		Expect(os.WriteFile(dir, nil, 0o644)).To(Succeed())
	}

	It("gives the flips with the error of each log it cannot read", func(ctx SpecContext) {
		spoiled := filepath.Join(runDir(env.Data(), runID), "attempt-2", "jobs", "111221661475_flaky")
		spoil(spoiled)

		got, err := ix.RerunFlips(ctx, index.Filter{SHAs: []string{"1a51097"}})
		Expect(err).To(HaveOccurred())
		Expect(strings.Count(err.Error(), spoiled)).To(Equal(1), "the job flip and its step flip share the log")
		Expect(got).To(ContainElement(SatisfyAll(flaky(), HaveField("Flip.Logs", HaveLen(2)))))
	})

	It("reads only the logs of the flips it gives", func(ctx SpecContext) {
		passes, err := filepath.Glob(filepath.Join(runDir(env.Data(), runID), "attempt-*", "jobs", "*_pass"))
		Expect(err).NotTo(HaveOccurred())
		Expect(passes).NotTo(BeEmpty())
		for _, dir := range passes {
			spoil(dir)
		}

		Expect(flips(ctx, index.Filter{SHAs: []string{"1a51097"}})).To(ContainElement(flaky()))
	})

	It("orders failing steps and step flips by step number", func(ctx SpecContext) {
		Expect(flips(ctx, index.Filter{SHAs: []string{"3"}})).To(HaveExactElements(
			SatisfyAll(HaveField("Flip.Step", ""), HaveField("Flip.FailingSteps", []string{stepEmit, stepBuild})),
			HaveField("Flip.Step", stepEmit),
			HaveField("Flip.Step", stepBuild),
		))
	})

	It("selects job names with Filter.Jobs, and runs with the rest of it, comparing every attempt of a run selected", func(ctx SpecContext) {
		Expect(flips(ctx, index.Filter{Jobs: []string{"time*"}})).To(SatisfyAll(HaveLen(2), HaveEach(HaveField("Flip.Job", "timeout"))))
		Expect(flips(ctx, index.Filter{SHAs: []string{"2"}})).To(HaveEach(HaveField("Flip.RunID", BeEquivalentTo(carried))))

		sha := []string{"1a51097"}
		betweenAttempts1And2 := time.Date(2026, 10, 3, 14, 24, 0, 0, time.UTC)
		Expect(flips(ctx, index.Filter{SHAs: sha, Since: betweenAttempts1And2})).To(ContainElement(flaky()))
		Expect(flips(ctx, index.Filter{SHAs: sha, Until: betweenAttempts1And2})).To(ContainElement(flaky()))
		Expect(flips(ctx, index.Filter{SHAs: sha, Since: time.Date(2026, 10, 3, 14, 27, 0, 0, time.UTC)})).To(BeEmpty())

		// The fixture run's latest attempt succeeded; the others' failed.
		Expect(flips(ctx, index.Filter{Conclusions: []string{"success"}})).To(SatisfyAll(ContainElement(flaky()), HaveEach(HaveField("Flip.RunID", BeEquivalentTo(runID)))))
		Expect(flips(ctx, index.Filter{Conclusions: []string{"failure"}})).To(SatisfyAll(Not(BeEmpty()), HaveEach(HaveField("Flip.RunID", Not(BeEquivalentTo(runID))))))
	})
})
