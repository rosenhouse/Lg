package index_test

import (
	"context"
	"encoding/json"
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

var _ = Describe("index.IntermittentFailures", Label("flakes"), func() {
	var (
		env          *harness.InProcessEnv
		ix           *index.Index
		intermittent scenario.IntermittentRuns
	)

	// untouched is a job that Intermittent leaves as recorded, so it succeeds in each of its runs.
	const untouched = "build (ubuntu-latest, 1.23)"
	// onMain is a run on main triggered by event, six hours after run afterRun of Intermittent.
	onMain := func(id int64, event string, afterRun int) scenario.Run {
		r := scenario.CloneAt(id, "after-attempt-1", time.Date(2026, 9, 27+afterRun, 18, 0, 0, 0, time.UTC))
		return scenario.WithEvent(scenario.OnBranch(r, "main"), event)
	}
	failingUntouched := func(id int64, event string, afterRun int) scenario.Run {
		r := onMain(id, event, afterRun)
		return scenario.SetJobConclusion(r, 1, r.JobIDs(1, untouched)[0], "failure")
	}

	BeforeEach(func(ctx SpecContext) {
		env = harness.InProcess()
		intermittent = scenario.Intermittent()
		runs := append(intermittent.All(), failingUntouched(9, "pull_request", 2), failingUntouched(10, "pull_request_target", 5))
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

	addRuns := func(ctx context.Context, runs ...scenario.Run) {
		GinkgoHelper()
		for _, r := range runs {
			Expect(env.Fake.AddRun(r)).To(Succeed())
		}
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(ix.Reconcile(ctx)).To(Succeed())
	}
	failures := func(ctx context.Context, f index.Filter) []index.Intermittent {
		GinkgoHelper()
		found, err := ix.IntermittentFailures(ctx, f)
		Expect(err).NotTo(HaveOccurred())
		return found
	}
	main := index.Filter{Branches: []string{"main"}}
	onlyMain := func(f index.Filter) index.Filter {
		f.Branches = main.Branches
		return f
	}

	runIDs := func(ids ...int64) types.GomegaMatcher {
		var each []types.GomegaMatcher
		for _, id := range ids {
			each = append(each, HaveField("RunID", id))
		}
		return HaveField("Runs", HaveExactElements(each))
	}
	failedAlone := func(job, step string, runs ...int64) types.GomegaMatcher {
		var each []types.GomegaMatcher
		for _, id := range runs {
			each = append(each, HaveField("RunID", id))
		}
		return SatisfyAll(HaveField("Job", job), HaveField("Step", step), HaveField("Failures", HaveExactElements(each)))
	}
	integration := failedAlone("integration", "", 3)
	unit := failedAlone("suite", "unit", 4)
	attempt1Log := func(run int64, job string) string {
		GinkgoHelper()
		id := intermittent.Main[run-1].JobIDs(1, job)[0]
		return filepath.Join(runDir(env.Data(), run), "attempt-1", "jobs", fmt.Sprintf("%d_%s", id, job), "log.txt")
	}

	It("gives each job and step of the branch that failed alone in a first attempt, with its series and the logs of those failures", func(ctx SpecContext) {
		outcome := func(run int64, conclusion string) types.GomegaMatcher {
			return SatisfyAll(HaveField("RunID", run), HaveField("HeadSHA", strings.Repeat(fmt.Sprint(run), 40)), HaveField("Conclusion", conclusion))
		}
		Expect(failures(ctx, main)).To(ConsistOf(
			SatisfyAll(
				integration,
				HaveField("Runs", HaveExactElements(
					outcome(1, "success"), outcome(2, "success"), outcome(3, "failure"),
					outcome(4, "success"), outcome(5, "success"), outcome(6, "success"),
				)),
				HaveField("Failures", HaveExactElements(SatisfyAll(outcome(3, "failure"), HaveField("Logs", []string{attempt1Log(3, "integration")})))),
				HaveField("Workflow", "lg-fixture"), HaveField("WorkflowID", BeEquivalentTo(373958224)), HaveField("Branch", "main"),
			),
			SatisfyAll(unit, runIDs(1, 2, 3, 4, 5, 6)),
		))
	})

	It("orders each series by the start of the first attempt, not by run id", func(ctx SpecContext) {
		addRuns(ctx, failingUntouched(20, "push", 1))

		Expect(failures(ctx, main)).To(ContainElement(SatisfyAll(failedAlone(untouched, "", 20), runIDs(1, 20, 2, 3, 4, 5, 6))))
	})

	It("names an attempt whose run_started_at is not a time", func(ctx SpecContext) {
		_, err := openDB(dbPath(env)).ExecContext(ctx, "UPDATE attempts SET run_started_at = 'yesterday'")
		Expect(err).NotTo(HaveOccurred())

		_, err = ix.IntermittentFailures(ctx, main)
		Expect(err).To(MatchError(ContainSubstring(`"yesterday"`)))
	})

	It("orders an attempt with no run_started_at first", func(ctx SpecContext) {
		_, err := openDB(dbPath(env)).ExecContext(ctx, "UPDATE attempts SET run_started_at = NULL WHERE path = ?", filepath.Join(runDir(env.Data(), 4), "attempt-1"))
		Expect(err).NotTo(HaveOccurred())

		Expect(failures(ctx, main)).To(ConsistOf(SatisfyAll(integration, runIDs(4, 1, 2, 3, 5, 6))))
	})

	It("leaves out pull_request and pull_request_target runs from the repository, and runs whose first attempt was cancelled", func(ctx SpecContext) {
		Expect(failures(ctx, index.Filter{})).To(ConsistOf(integration, unit))
	})

	It("leaves out a run whose first attempt failed and whose latest attempt was cancelled", func(ctx SpecContext) {
		r := scenario.AddRerunAttempt(failingUntouched(12, "push", 3), untouched)
		addRuns(ctx, scenario.Cancel(r, 2))

		Expect(failures(ctx, main)).To(ConsistOf(integration, unit))
	})

	It("leaves out a run whose first attempt was cancelled and whose latest attempt succeeded", func(ctx SpecContext) {
		r := scenario.Cancel(scenario.AddRerunAttempt(onMain(12, "push", 3), untouched), 1)
		addRuns(ctx, scenario.SetJobConclusion(r, 1, r.JobIDs(1, untouched)[0], "cancelled"))

		Expect(failures(ctx, main)).To(ConsistOf(integration, unit))
	})

	// withoutFirstAttempt is a run re-run after run afterRun of Intermittent whose first attempt sync cannot fetch.
	withoutFirstAttempt := func(id int64, event string, afterRun int) scenario.Run {
		r := scenario.AddRerunAttempt(scenario.WithSHA(onMain(id, event, afterRun), strings.Repeat("e", 40)), untouched)
		env.Fake.Fail("api", fmt.Sprintf("jobs/%d/logs", r.JobIDs(1, untouched)[0]), fakegithub.Fault{Status: http.StatusInternalServerError})
		return r
	}
	addRunsWithoutFirstAttempt := func(ctx context.Context, runs ...scenario.Run) {
		GinkgoHelper()
		for _, r := range runs {
			Expect(env.Fake.AddRun(r)).To(Succeed())
		}
		Expect(env.Sync(ctx)).NotTo(Succeed())
		Expect(ix.Reconcile(ctx)).To(Succeed())
		for _, r := range runs {
			Expect(filepath.Join(runDir(env.Data(), r.ID), "attempt-1")).NotTo(BeAnExistingFile())
			Expect(filepath.Join(runDir(env.Data(), r.ID), "attempt-2")).To(BeADirectory())
		}
	}

	It("treats a run whose first attempt is not on disk as a gap in each series of its workflow and branch", func(ctx SpecContext) {
		addRunsWithoutFirstAttempt(ctx, withoutFirstAttempt(14, "push", 3))

		Expect(failures(ctx, main)).To(BeEmpty())
	})

	It("names a run whose first attempt is not on disk and whose created_at is not a time", func(ctx SpecContext) {
		addRunsWithoutFirstAttempt(ctx, withoutFirstAttempt(14, "push", 3))
		_, err := openDB(dbPath(env)).ExecContext(ctx, "UPDATE runs SET created_at = 'yesterday' WHERE run_id = 14")
		Expect(err).NotTo(HaveOccurred())

		_, err = ix.IntermittentFailures(ctx, main)
		Expect(err).To(MatchError(SatisfyAll(ContainSubstring(runDir(env.Data(), 14)), ContainSubstring(`"yesterday"`))))
	})

	It("orders a run whose first attempt is not on disk and that has no created_at first", func(ctx SpecContext) {
		addRunsWithoutFirstAttempt(ctx, withoutFirstAttempt(14, "push", 3))
		_, err := openDB(dbPath(env)).ExecContext(ctx, "UPDATE runs SET created_at = NULL WHERE run_id = 14")
		Expect(err).NotTo(HaveOccurred())

		Expect(failures(ctx, main)).To(ConsistOf(SatisfyAll(integration, runIDs(14, 1, 2, 3, 4, 5, 6),
			HaveField("Runs", ContainElement(model.RunOutcome{RunID: 14, HeadSHA: strings.Repeat("e", 40)}))), unit))
	})

	It("leaves out pull_request runs and runs whose latest attempt was cancelled, though their first attempt is not on disk", func(ctx SpecContext) {
		addRunsWithoutFirstAttempt(ctx, withoutFirstAttempt(15, "pull_request", 3), scenario.Cancel(withoutFirstAttempt(16, "push", 3), 2))

		Expect(failures(ctx, main)).To(ConsistOf(integration, unit))
	})

	It("ignores attempts after the first, so a job that failed only in a re-run did not fail alone", func(ctx SpecContext) {
		r := scenario.AddRerunAttempt(onMain(13, "push", 3), untouched)
		addRuns(ctx, scenario.SetJobConclusion(r, 2, r.JobIDs(2, untouched)[0], "failure"))

		Expect(failures(ctx, main)).To(ConsistOf(integration, unit))
	})

	It("leaves out runs from forks when given Filter.Branches", func(ctx SpecContext) {
		addRuns(ctx, scenario.FromFork(failingUntouched(11, "push", 3), "someone/Lg"))

		Expect(failures(ctx, main)).To(ConsistOf(integration, unit))
	})

	It("builds each series from every run of the branches, workflows and events f selects, and gives the failures of the runs the rest of f selects", func(ctx SpecContext) {
		run4 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		Expect(failures(ctx, onlyMain(index.Filter{SHAs: []string{"4"}}))).To(ConsistOf(SatisfyAll(unit, runIDs(1, 2, 3, 4, 5, 6))))
		Expect(failures(ctx, onlyMain(index.Filter{Since: run4, Until: run4}))).To(ConsistOf(SatisfyAll(unit, runIDs(1, 2, 3, 4, 5, 6))))
		Expect(failures(ctx, onlyMain(index.Filter{Conclusions: []string{"failure"}}))).To(ConsistOf(integration, unit))
		Expect(failures(ctx, onlyMain(index.Filter{Conclusions: []string{"success"}}))).To(BeEmpty())
		Expect(failures(ctx, onlyMain(index.Filter{PRs: []int{42}}))).To(BeEmpty())
		Expect(failures(ctx, onlyMain(index.Filter{Workflows: []string{"lg-fixture"}, Events: []string{"push"}}))).To(ConsistOf(integration, unit))
		Expect(failures(ctx, onlyMain(index.Filter{Events: []string{"schedule"}}))).To(BeEmpty())
		Expect(failures(ctx, onlyMain(index.Filter{Workflows: []string{"other"}}))).To(BeEmpty())
		Expect(failures(ctx, index.Filter{Branches: []string{"release-3"}})).To(BeEmpty())
	})

	It("matches Since and Until against the start of any attempt of a run", func(ctx SpecContext) {
		var rerun struct {
			RunStartedAt time.Time `json:"run_started_at"`
		}
		Expect(json.Unmarshal(intermittent.Main[2].Files["attempt-2/attempt.json"].Data, &rerun)).To(Succeed())

		Expect(failures(ctx, onlyMain(index.Filter{Since: rerun.RunStartedAt, Until: rerun.RunStartedAt}))).To(ConsistOf(integration))
	})

	It("selects job names with Filter.Jobs", func(ctx SpecContext) {
		Expect(failures(ctx, onlyMain(index.Filter{Jobs: []string{"integ*"}}))).To(ConsistOf(integration))
	})

	It("lists only failures' logs that are regular files, with the error of each it cannot read, and leaves the series' logs as they were", func(ctx SpecContext) {
		Expect(os.Remove(attempt1Log(3, "integration"))).To(Succeed())
		spoiled := filepath.Dir(attempt1Log(4, "suite"))
		Expect(os.RemoveAll(spoiled)).To(Succeed())
		Expect(os.WriteFile(spoiled, nil, 0o644)).To(Succeed())

		got, err := ix.IntermittentFailures(ctx, main)
		Expect(err).To(MatchError(ContainSubstring(spoiled)))
		Expect(got).To(ContainElement(SatisfyAll(
			integration,
			HaveField("Failures", HaveEach(HaveField("Logs", BeEmpty()))),
			HaveField("Runs", ContainElement(HaveField("Logs", []string{attempt1Log(3, "integration")}))),
		)))
	})

	It("reads no log of a failure that was not alone", func(ctx SpecContext) {
		spoiled := filepath.Dir(attempt1Log(4, "broken"))
		Expect(os.RemoveAll(spoiled)).To(Succeed())
		Expect(os.WriteFile(spoiled, nil, 0o644)).To(Succeed())

		Expect(failures(ctx, main)).To(ConsistOf(integration, unit))
	})
})
