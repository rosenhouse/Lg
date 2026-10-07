package model_test

import (
	"fmt"
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
func firstAttempt(run int64, hour int, id int64, name, conclusion string, steps ...string) model.FirstAttemptJob {
	return model.FirstAttemptJob{
		AttemptJob: job(run, 1, id, name, conclusion, steps...),
		HeadSHA:    fmt.Sprint("sha", run),
		WorkflowID: 100, Workflow: "ci", Branch: "main",
		StartedAt: time.Date(2026, 10, 1, hour, 0, 0, 0, time.UTC),
	}
}

func inWorkflow(j model.FirstAttemptJob, id int64, name string) model.FirstAttemptJob {
	j.WorkflowID, j.Workflow = id, name
	return j
}

func onBranch(j model.FirstAttemptJob, branch string) model.FirstAttemptJob {
	j.Branch = branch
	return j
}

func ran(run int64, conclusion string, logs ...string) model.RunOutcome {
	return model.RunOutcome{RunID: run, HeadSHA: fmt.Sprint("sha", run), Conclusion: conclusion, Logs: logs}
}

var _ = Describe("FirstAttemptSeries", Label("flakes"), func() {
	It("groups outcomes by workflow_id, branch and job name, and by step name within them, ordered by run_started_at", func() {
		series := model.FirstAttemptSeries([]model.FirstAttemptJob{
			firstAttempt(3, 1, 31, "test", "success", "go test", "success"),
			// Workflow 100 is renamed by run 1, the latest.
			inWorkflow(firstAttempt(1, 3, 11, "test", "failure", "go test", "failure"), 100, "CI"),
			firstAttempt(2, 2, 21, "test", "success", "go test", "success"),
			inWorkflow(firstAttempt(4, 4, 41, "test", "failure"), 200, "release"),
			onBranch(firstAttempt(5, 5, 51, "test", "failure"), "release-3"),
		})

		Expect(series).To(HaveExactElements(
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
		series := model.FirstAttemptSeries([]model.FirstAttemptJob{
			firstAttempt(1, 1, 11, "test", "failure", "unit", "failure", "e2e", "skipped"),
			firstAttempt(1, 1, 12, "test", "success", "unit", "success", "e2e", "success"),
			firstAttempt(2, 2, 21, "test", "success", "unit", "success", "e2e", "success"),
			firstAttempt(2, 2, 22, "test", "neutral"),
			notApplicable,
		})

		Expect(series).To(HaveExactElements(
			HaveField("Runs", []model.RunOutcome{ran(1, "failure", "attempt-1/11/log.txt"), ran(2, "success")}),
			SatisfyAll(HaveField("Step", "unit"), HaveField("Runs", []model.RunOutcome{ran(1, "failure", "attempt-1/11/log.txt"), ran(2, "success")})),
			SatisfyAll(HaveField("Step", "e2e"), HaveField("Runs", []model.RunOutcome{ran(1, "success"), ran(2, "success")})),
		))
	})
})
