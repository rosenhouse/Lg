package model_test

import (
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
)

// conclusions spells S as success, F as failure, C as cancelled and T as timed_out.
func conclusions(letters string) []string {
	names := map[rune]string{'S': "success", 'F': "failure", 'C': "cancelled", 'T': "timed_out"}
	var out []string
	for _, letter := range letters {
		out = append(out, names[letter])
	}
	return out
}

var _ = Describe("IsolatedFailures", Label("flakes"), func() {
	DescribeTable("gives the index of each failure between two successes",
		func(letters string, want []int) {
			Expect(model.IsolatedFailures(conclusions(letters))).To(Equal(want))
		},
		Entry("SSFSS", "SSFSS", []int{2}),
		Entry("SSSFF", "SSSFF", nil),
		Entry("FSS", "FSS", nil),
		Entry("SFSFS", "SFSFS", []int{1, 3}),
		Entry("S", "S", nil),
		Entry("none", "", nil),
		Entry("SSF", "SSF", nil),
		Entry("SFFS", "SFFS", nil),
		Entry("SCS, as cancelled fails", "SCS", []int{1}),
		Entry("STS, as timed_out fails", "STS", []int{1}),
	)
})

// firstAttempt is a job that ran in the first attempt of run on main of workflow 100,
// which started hour hours after midnight.
func firstAttempt(run int64, hour int, id int64, name, conclusion string, steps ...string) model.RunJob {
	return model.RunJob{
		AttemptJob: job(run, 1, id, name, conclusion, steps...),
		HeadSHA:    fmt.Sprint("sha", run),
		WorkflowID: 100, Workflow: "ci", Branch: "main",
		StartedAt: time.Date(2026, 10, 1, hour, 0, 0, 0, time.UTC),
	}
}

func inWorkflow(j model.RunJob, id int64, name string) model.RunJob {
	j.WorkflowID, j.Workflow = id, name
	return j
}

func onBranch(j model.RunJob, branch string) model.RunJob {
	j.Branch = branch
	return j
}

func ran(run int64, conclusion string, logs ...string) model.RunOutcome {
	return model.RunOutcome{RunID: run, HeadSHA: fmt.Sprint("sha", run), Conclusion: conclusion, Logs: logs}
}

var _ = Describe("FirstAttemptSeries", Label("flakes"), func() {
	It("groups outcomes by workflow_id, branch, job name and step name, ordered by run_started_at, and orders the series by branch, workflow_id and job name", func() {
		series := model.FirstAttemptSeries([]model.RunJob{
			firstAttempt(3, 1, 31, "test", "success", "go test", "success"),
			// Workflow 100 is renamed by run 1, the latest.
			inWorkflow(firstAttempt(1, 3, 11, "test", "failure", "go test", "failure"), 100, "CI"),
			inWorkflow(firstAttempt(1, 3, 12, "build", "success"), 100, "CI"),
			firstAttempt(2, 2, 21, "test", "success", "go test", "success"),
			inWorkflow(firstAttempt(4, 0, 41, "test", "failure"), 200, "release"),
			onBranch(firstAttempt(5, 0, 51, "test", "failure"), "release-3"),
		}, nil)

		Expect(series).To(HaveExactElements(
			model.Series{WorkflowID: 100, Workflow: "CI", Branch: "main", Job: "build", Runs: []model.RunOutcome{ran(1, "success")}},
			model.Series{WorkflowID: 100, Workflow: "CI", Branch: "main", Job: "test", Runs: []model.RunOutcome{
				ran(3, "success"), ran(2, "success"), ran(1, "failure", "attempt-1/11/log.txt"),
			}},
			model.Series{WorkflowID: 100, Workflow: "CI", Branch: "main", Job: "test", Step: "go test", Runs: []model.RunOutcome{
				ran(3, "success"), ran(2, "success"), ran(1, "failure", "attempt-1/11/log.txt"),
			}},
			model.Series{WorkflowID: 200, Workflow: "release", Branch: "main", Job: "test", Runs: []model.RunOutcome{
				ran(4, "failure", "attempt-1/41/log.txt"),
			}},
			model.Series{WorkflowID: 100, Workflow: "ci", Branch: "release-3", Job: "test", Runs: []model.RunOutcome{
				ran(5, "failure", "attempt-1/51/log.txt"),
			}},
		))
	})

	It("ignores skipped steps and jobs that did not run, and fails a name when any job of it failed", func() {
		notApplicable := firstAttempt(3, 3, 31, "test", "failure", "unit", "failure")
		notApplicable.Kind = model.NotApplicable
		series := model.FirstAttemptSeries([]model.RunJob{
			firstAttempt(1, 1, 11, "test", "failure", "unit", "failure", "e2e", "skipped"),
			firstAttempt(1, 1, 12, "test", "success", "unit", "success", "e2e", "success"),
			firstAttempt(2, 2, 21, "test", "success", "unit", "success", "e2e", "success"),
			firstAttempt(2, 2, 22, "test", "neutral"),
			notApplicable,
		}, nil)

		Expect(series).To(HaveExactElements(
			HaveField("Runs", []model.RunOutcome{ran(1, "failure", "attempt-1/11/log.txt"), ran(2, "success")}),
			SatisfyAll(HaveField("Step", "unit"), HaveField("Runs", []model.RunOutcome{ran(1, "failure", "attempt-1/11/log.txt"), ran(2, "success")})),
			SatisfyAll(HaveField("Step", "e2e"), HaveField("Runs", []model.RunOutcome{ran(1, "success"), ran(2, "success")})),
		))
	})
	It("leaves out of a series a run whose only job of the name was neutral, or skipped the step", func() {
		series := model.FirstAttemptSeries([]model.RunJob{
			firstAttempt(1, 1, 11, "test", "success", "e2e", "success"),
			firstAttempt(1, 1, 12, "lint", "success"),
			firstAttempt(2, 2, 21, "test", "success", "e2e", "skipped"),
			firstAttempt(2, 2, 22, "lint", "neutral"),
			firstAttempt(3, 3, 31, "test", "failure", "e2e", "failure"),
			firstAttempt(3, 3, 32, "lint", "failure"),
		}, nil)

		Expect(series).To(HaveExactElements(
			SatisfyAll(HaveField("Job", "lint"), HaveField("Runs", []model.RunOutcome{ran(1, "success"), ran(3, "failure", "attempt-1/32/log.txt")})),
			SatisfyAll(HaveField("Job", "test"), HaveField("Step", ""), HaveField("Runs", HaveLen(3))),
			SatisfyAll(HaveField("Job", "test"), HaveField("Step", "e2e"), HaveField("Runs", []model.RunOutcome{ran(1, "success"), ran(3, "failure", "attempt-1/31/log.txt")})),
		))
	})

	It("gives a step series per job name, though two job names share the step name", func() {
		series := model.FirstAttemptSeries([]model.RunJob{
			firstAttempt(1, 1, 11, "test", "failure", "go test", "failure"),
			firstAttempt(1, 1, 12, "lint", "success", "go test", "success"),
		}, nil)

		Expect(series).To(HaveExactElements(
			HaveField("Job", "lint"),
			SatisfyAll(HaveField("Job", "lint"), HaveField("Step", "go test"), HaveField("Runs", []model.RunOutcome{ran(1, "success")})),
			HaveField("Job", "test"),
			SatisfyAll(HaveField("Job", "test"), HaveField("Step", "go test"), HaveField("Runs", []model.RunOutcome{ran(1, "failure", "attempt-1/11/log.txt")})),
		))
	})

	It("gives each run once, though another run started in the same second", func() {
		series := model.FirstAttemptSeries([]model.RunJob{
			firstAttempt(1, 1, 11, "test", "success"),
			firstAttempt(2, 1, 12, "test", "success"),
			firstAttempt(1, 1, 13, "test", "success"),
		}, nil)

		Expect(series).To(HaveExactElements(HaveField("Runs", []model.RunOutcome{ran(1, "success"), ran(2, "success")})))
	})

	It("gives a run's failing logs in the order of their job ids", func() {
		series := model.FirstAttemptSeries([]model.RunJob{
			firstAttempt(1, 1, 12, "test", "failure"),
			firstAttempt(1, 1, 11, "test", "failure"),
		}, nil)

		Expect(series).To(HaveExactElements(HaveField("Runs", []model.RunOutcome{ran(1, "failure", "attempt-1/11/log.txt", "attempt-1/12/log.txt")})))
	})

	It("places each gap by its time, concluding \"\", in each series of its workflow and branch", func() {
		series := model.FirstAttemptSeries([]model.RunJob{
			firstAttempt(1, 1, 11, "test", "success"),
			firstAttempt(3, 3, 31, "test", "failure"),
			firstAttempt(4, 4, 41, "test", "success"),
			inWorkflow(firstAttempt(5, 1, 51, "test", "success"), 200, "release"),
		}, []model.Gap{
			{RunID: 2, HeadSHA: "sha2", WorkflowID: 100, Branch: "main", At: time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)},
			{RunID: 6, HeadSHA: "sha6", WorkflowID: 100, Branch: "release-3", At: time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)},
			{RunID: 7, HeadSHA: "sha7", WorkflowID: 200, Branch: "main", At: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		})

		Expect(series).To(HaveExactElements(
			SatisfyAll(HaveField("WorkflowID", BeEquivalentTo(100)), HaveField("Runs", []model.RunOutcome{
				ran(1, "success"), ran(2, ""), ran(3, "failure", "attempt-1/31/log.txt"), ran(4, "success"),
			})),
			SatisfyAll(HaveField("WorkflowID", BeEquivalentTo(200)), HaveField("Runs", []model.RunOutcome{ran(7, ""), ran(5, "success")})),
		))
	})

	It("leaves its input as it was", func() {
		notApplicable := firstAttempt(1, 1, 12, "test", "failure")
		notApplicable.Kind = model.NotApplicable
		jobs := []model.RunJob{firstAttempt(2, 2, 21, "test", "success"), notApplicable, firstAttempt(1, 1, 11, "test", "failure")}
		given := slices.Clone(jobs)

		model.FirstAttemptSeries(jobs, nil)

		Expect(jobs).To(Equal(given))
	})
})

var _ = Describe("Series.IsolatedFailures", Label("flakes"), func() {
	It("gives the runs whose failure fell between two successes", func() {
		s := model.Series{Runs: []model.RunOutcome{
			ran(1, "success"), ran(2, "cancelled", "2.txt"), ran(3, "success"), ran(4, "failure", "4.txt"), ran(5, "failure", "5.txt"),
		}}

		Expect(s.IsolatedFailures()).To(HaveExactElements(ran(2, "cancelled", "2.txt")))
	})
})
