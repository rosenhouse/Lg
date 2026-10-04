package model_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = Describe("Classify", Label("sync"), func() {
	runner := "GitHub Actions 1000002376"
	step := model.Step{Name: "Run tests"}
	runStarted := time.Date(2026, 10, 3, 14, 25, 8, 0, time.UTC)
	before := runStarted.Add(-time.Second)
	after := runStarted.Add(time.Second)

	DescribeTable("gives not_applicable only when steps is empty and runner_name is null",
		func(job model.Job, kind model.JobKind) {
			Expect(model.Classify(job, runStarted)).To(Equal(kind))
		},
		Entry("no steps, no runner", model.Job{Steps: []model.Step{}}, model.NotApplicable),
		Entry("no steps, a runner", model.Job{Steps: []model.Step{}, RunnerName: &runner}, model.Ran),
		Entry("steps, no runner", model.Job{Steps: []model.Step{step}}, model.Ran),
		Entry("steps and a runner", model.Job{Steps: []model.Step{step}, RunnerName: &runner}, model.Ran),
	)

	DescribeTable("gives carried_forward only when started_at is set and earlier than the attempt's run_started_at", Label("attempts"),
		func(startedAt *time.Time, kind model.JobKind) {
			Expect(model.Classify(model.Job{StartedAt: startedAt, Steps: []model.Step{step}, RunnerName: &runner}, runStarted)).To(Equal(kind))
		},
		Entry("earlier", &before, model.CarriedForward),
		Entry("equal", &runStarted, model.Ran),
		Entry("later", &after, model.Ran),
		Entry("null", nil, model.Ran),
	)

	It("gives carried_forward over not_applicable", Label("attempts"), func() {
		skipped := model.Job{StartedAt: &before, Steps: []model.Step{}}

		Expect(model.Classify(skipped, runStarted)).To(Equal(model.CarriedForward))
	})

	DescribeTable("carries forward 8 jobs of recorded attempt 2, and none of attempts 1 and 3", Label("attempts"),
		func(attempt string, carried int) {
			dir := filepath.Join(recordings.Dir(37129390741, "after-attempt-3"), attempt)
			var run model.Run
			readJSON(filepath.Join(dir, "attempt.json"), &run)
			var listing struct{ Jobs []model.Job }
			readJSON(filepath.Join(dir, "jobs.json"), &listing)

			kinds := map[model.JobKind]int{}
			for _, job := range listing.Jobs {
				kinds[model.Classify(job, run.RunStartedAt)]++
			}
			Expect(kinds[model.CarriedForward]).To(Equal(carried))
		},
		Entry("attempt 1", "attempt-1", 0),
		Entry("attempt 2", "attempt-2", 8),
		Entry("attempt 3", "attempt-3", 0),
	)
})

func readJSON(path string, v any) {
	GinkgoHelper()
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Unmarshal(raw, v)).To(Succeed())
}
