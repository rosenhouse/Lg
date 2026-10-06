package model_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
)

// job is a job of run's attempt that ran, with the steps given as name, conclusion pairs.
func job(run int64, attempt int, id int64, name, conclusion string, steps ...string) model.AttemptJob {
	j := model.AttemptJob{
		RunID: run, Attempt: attempt, Kind: model.Ran,
		Job: model.Job{ID: id, Name: name, Conclusion: conclusion},
		Log: fmt.Sprintf("attempt-%d/%d/log.txt", attempt, id),
	}
	for i := 0; i+1 < len(steps); i += 2 {
		j.Job.Steps = append(j.Job.Steps, model.Step{Number: i/2 + 1, Name: steps[i], Conclusion: steps[i+1]})
	}
	return j
}

func kind(j model.AttemptJob, k model.JobKind) model.AttemptJob {
	j.Kind, j.Log = k, ""
	return j
}

func outcomes(pairs ...any) []model.Outcome {
	var out []model.Outcome
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, model.Outcome{Attempt: pairs[i].(int), Conclusion: pairs[i+1].(string)})
	}
	return out
}

var _ = Describe("RerunFlips", Label("flakes"), func() {
	It("groups by (run, job name, attempt) and by (run, job name, step name, attempt) over jobs that ran", func() {
		flips := model.RerunFlips([]model.AttemptJob{
			job(1, 1, 11, "test", "failure", "go test", "failure"),
			job(1, 2, 21, "test", "success", "go test", "success"),
			// Run 2 never flips, though run 1 failed and run 2 passed.
			job(2, 1, 12, "test", "success", "go test", "success"),
			// Carried forward from attempt 1, and not applicable.
			kind(job(1, 2, 22, "lint", "failure", "golangci-lint", "failure"), model.CarriedForward),
			job(1, 1, 13, "lint", "failure", "golangci-lint", "failure"),
			job(1, 3, 33, "lint", "success", "golangci-lint", "success"),
			kind(job(1, 1, 14, "deploy", "failure"), model.NotApplicable),
			job(1, 2, 24, "deploy", "success"),
		})

		Expect(flips).To(HaveExactElements(
			model.Flip{RunID: 1, Job: "lint", Outcomes: outcomes(1, "failure", 3, "success"), FailingSteps: []string{"golangci-lint"}, Logs: []string{"attempt-1/13/log.txt", "attempt-3/33/log.txt"}},
			model.Flip{RunID: 1, Job: "lint", Step: "golangci-lint", Outcomes: outcomes(1, "failure", 3, "success"), FailingSteps: []string{"golangci-lint"}, Logs: []string{"attempt-1/13/log.txt", "attempt-3/33/log.txt"}},
			model.Flip{RunID: 1, Job: "test", Outcomes: outcomes(1, "failure", 2, "success"), FailingSteps: []string{"go test"}, Logs: []string{"attempt-1/11/log.txt", "attempt-2/21/log.txt"}},
			model.Flip{RunID: 1, Job: "test", Step: "go test", Outcomes: outcomes(1, "failure", 2, "success"), FailingSteps: []string{"go test"}, Logs: []string{"attempt-1/11/log.txt", "attempt-2/21/log.txt"}},
		))
	})

	It("reports a step that flips while its job does not, and steps in the order the job runs them", func() {
		flips := model.RerunFlips([]model.AttemptJob{
			job(1, 2, 21, "test", "failure", "setup", "success", "A", "success", "B", "failure"),
			job(1, 1, 11, "test", "failure", "setup", "success", "A", "failure", "B", "skipped"),
			job(1, 3, 31, "test", "success", "setup", "success", "A", "failure", "B", "success"),
		})

		Expect(flips).To(HaveExactElements(
			HaveField("Job", "test"),
			SatisfyAll(HaveField("Step", "A"), HaveField("Outcomes", outcomes(1, "failure", 2, "success", 3, "failure"))),
			SatisfyAll(HaveField("Step", "B"), HaveField("Outcomes", outcomes(2, "failure", 3, "success"))),
		))
		Expect(flips[0].FailingSteps).To(Equal([]string{"A", "B"}))
	})

	It("fails a name's outcome in an attempt if any ran job of that name failed, and a step's if a step of that name failed in any ran job of that name", func() {
		flips := model.RerunFlips([]model.AttemptJob{
			job(1, 1, 11, "same name", "success", "check", "success"),
			job(1, 1, 12, "same name", "failure", "check", "failure"),
			job(1, 2, 21, "same name", "success", "check", "success"),
			job(1, 2, 22, "same name", "success", "check", "success"),
		})

		Expect(flips).To(HaveExactElements(
			SatisfyAll(HaveField("Step", ""), HaveField("Outcomes", outcomes(1, "failure", 2, "success")), HaveField("Logs", HaveLen(4))),
			SatisfyAll(HaveField("Step", "check"), HaveField("Outcomes", outcomes(1, "failure", 2, "success"))),
		))
	})

	It("takes a step's outcome from the step, not from its job", func() {
		flips := model.RerunFlips([]model.AttemptJob{
			job(1, 1, 11, "test", "success", "check", "failure"),
			job(1, 2, 21, "test", "success", "check", "success"),
		})

		Expect(flips).To(HaveExactElements(HaveField("Step", "check")))
	})

	It("gives a failing outcome the first failing conclusion by job id", func() {
		flips := model.RerunFlips([]model.AttemptJob{
			job(1, 1, 12, "same name", "cancelled"),
			job(1, 1, 11, "same name", "timed_out"),
			job(1, 2, 21, "same name", "success"),
		})

		Expect(flips).To(HaveExactElements(HaveField("Outcomes", outcomes(1, "timed_out", 2, "success"))))
	})

	DescribeTable("fails failure, cancelled and timed_out, and ignores skipped and neutral",
		func(conclusion string, want []model.Outcome) {
			flips := model.RerunFlips([]model.AttemptJob{
				job(1, 1, 11, "test", conclusion, "check", conclusion),
				job(1, 2, 21, "test", "success", "check", "success"),
				job(1, 3, 31, "test", "failure", "check", "failure"),
			})

			Expect(flips).To(HaveExactElements(HaveField("Outcomes", want), HaveField("Outcomes", want)))
		},
		Entry("failure", "failure", outcomes(1, "failure", 2, "success", 3, "failure")),
		Entry("cancelled", "cancelled", outcomes(1, "cancelled", 2, "success", 3, "failure")),
		Entry("timed_out", "timed_out", outcomes(1, "timed_out", 2, "success", 3, "failure")),
		Entry("skipped", "skipped", outcomes(2, "success", 3, "failure")),
		Entry("neutral", "neutral", outcomes(2, "success", 3, "failure")),
	)

	It("reports nothing for a name that only failed, or only passed and was skipped", func() {
		Expect(model.RerunFlips([]model.AttemptJob{
			job(1, 1, 11, "broken", "failure"),
			job(1, 2, 21, "broken", "cancelled"),
			job(1, 1, 12, "upload", "skipped"),
			job(1, 2, 22, "upload", "success"),
		})).To(BeEmpty())
	})
})
