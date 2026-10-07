package index_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("index.FirstAttemptOutcomes", Label("flakes"), func() {
	var (
		env          *harness.InProcessEnv
		ix           *index.Index
		intermittent scenario.IntermittentRuns
	)

	// steady is a job that succeeds in each of Intermittent's runs.
	const steady = "build (ubuntu-latest, 1.23)"
	// failingSteady is a run on main triggered by the event, a little after
	// Intermittent's run after, where steady fails.
	failingSteady := func(id int64, event string, after int) scenario.Run {
		r := scenario.CloneAt(id, "after-attempt-1", time.Date(2026, 9, 28+after, 18, 0, 0, 0, time.UTC))
		r = scenario.WithEvent(scenario.OnBranch(r, "main"), event)
		return scenario.SetJobConclusion(r, 1, r.JobIDs(1, steady)[0], "failure")
	}

	BeforeEach(func(ctx SpecContext) {
		env = harness.InProcess()
		intermittent = scenario.Intermittent()
		runs := append(intermittent.All(), failingSteady(9, "pull_request", 1), failingSteady(10, "pull_request_target", 4))
		for _, r := range runs {
			Expect(env.Fake.AddRun(r)).To(Succeed())
		}
		Expect(env.Sync(ctx)).To(Succeed())
		var err error
		ix, err = index.Open(ctx, dbPath(env), env.Data(), nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)
		Expect(ix.Reconcile(ctx)).To(Succeed())
	}, syncTimeout)

	outcomes := func(ctx context.Context, f index.Filter) []model.Series {
		GinkgoHelper()
		series, err := ix.FirstAttemptOutcomes(ctx, f)
		Expect(err).NotTo(HaveOccurred())
		return series
	}
	main := index.Filter{Branches: []string{"main"}}

	runIDs := func(ids ...int64) types.GomegaMatcher {
		var each []types.GomegaMatcher
		for _, id := range ids {
			each = append(each, HaveField("RunID", id))
		}
		return HaveField("Runs", HaveExactElements(each))
	}
	series := func(job, step string, m types.GomegaMatcher) types.GomegaMatcher {
		return SatisfyAll(HaveField("Job", job), HaveField("Step", step), m)
	}
	attempt1Log := func(run int64, job string) string {
		GinkgoHelper()
		id := intermittent.Main[run-1].JobIDs(1, job)[0]
		return filepath.Join(runDir(env.Data(), run), "attempt-1", "jobs", fmt.Sprintf("%d_%s", id, job), "log.txt")
	}

	It("gives each job and step series of the branch from first-attempt conclusions, with the logs of the jobs that failed", func(ctx SpecContext) {
		Expect(outcomes(ctx, main)).To(ContainElements(
			series("integration", "", HaveField("Runs", []model.RunOutcome{
				{RunID: 1, HeadSHA: strings.Repeat("1", 40), Conclusion: "success"},
				{RunID: 2, HeadSHA: strings.Repeat("2", 40), Conclusion: "success"},
				{RunID: 3, HeadSHA: strings.Repeat("3", 40), Conclusion: "failure", Logs: []string{attempt1Log(3, "integration")}},
				{RunID: 4, HeadSHA: strings.Repeat("4", 40), Conclusion: "success"},
				{RunID: 5, HeadSHA: strings.Repeat("5", 40), Conclusion: "success"},
				{RunID: 6, HeadSHA: strings.Repeat("6", 40), Conclusion: "success"},
			})),
			series("suite", "e2e", runIDs(1, 2, 3, 5, 6)),
			SatisfyAll(HaveField("Workflow", "lg-fixture"), HaveField("WorkflowID", BeEquivalentTo(373958224)), HaveField("Branch", "main")),
		))
	})

	It("leaves out pull_request and pull_request_target runs from the repository, and runs whose first attempt was cancelled", func(ctx SpecContext) {
		Expect(outcomes(ctx, index.Filter{})).To(ContainElement(series(steady, "", runIDs(1, 2, 3, 4, 5, 6))))
	})

	It("leaves out runs from forks when given Filter.Branches", func(ctx SpecContext) {
		Expect(env.Fake.AddRun(scenario.FromFork(failingSteady(11, "push", 2), "someone/Lg"))).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())

		Expect(outcomes(ctx, main)).To(ContainElement(series(steady, "", runIDs(1, 2, 3, 4, 5, 6))))
	})

	It("selects job names with Filter.Jobs, and runs with the rest of it", func(ctx SpecContext) {
		Expect(outcomes(ctx, index.Filter{Branches: main.Branches, Jobs: []string{"integ*"}})).To(SatisfyAll(
			ContainElement(series("integration", "", runIDs(1, 2, 3, 4, 5, 6))),
			HaveEach(HaveField("Job", "integration")),
		))
		Expect(outcomes(ctx, index.Filter{Branches: []string{"release-3"}})).To(BeEmpty())
		Expect(outcomes(ctx, index.Filter{Branches: main.Branches, SHAs: []string{"3", "4"}})).To(SatisfyAll(Not(BeEmpty()), HaveEach(HaveField("Runs", HaveEach(HaveField("RunID", BeElementOf(int64(3), int64(4))))))))
	})

	It("lists only logs that are regular files, with the error of each it cannot read", func(ctx SpecContext) {
		Expect(os.Remove(attempt1Log(3, "integration"))).To(Succeed())
		spoiled := filepath.Dir(attempt1Log(4, "broken"))
		Expect(os.RemoveAll(spoiled)).To(Succeed())
		Expect(os.WriteFile(spoiled, nil, 0o644)).To(Succeed())

		got, err := ix.FirstAttemptOutcomes(ctx, main)
		Expect(err).To(MatchError(ContainSubstring(spoiled)))
		Expect(got).To(ContainElements(
			series("integration", "", HaveField("Runs", HaveEach(HaveField("Logs", BeEmpty())))),
			series("broken", "", HaveField("Runs", ContainElement(SatisfyAll(HaveField("RunID", BeEquivalentTo(5)), HaveField("Logs", HaveLen(1)))))),
		))
	})
})
