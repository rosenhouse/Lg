package model_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
)

var _ = Describe("Classify", Label("sync"), func() {
	runner := "GitHub Actions 1000002376"
	step := model.Step{Name: "Run tests"}

	DescribeTable("gives not_applicable only when steps is empty and runner_name is null",
		func(job model.Job, kind model.JobKind) {
			Expect(model.Classify(job)).To(Equal(kind))
		},
		Entry("no steps, no runner", model.Job{Steps: []model.Step{}}, model.NotApplicable),
		Entry("no steps, a runner", model.Job{Steps: []model.Step{}, RunnerName: &runner}, model.Ran),
		Entry("steps, no runner", model.Job{Steps: []model.Step{step}}, model.Ran),
		Entry("steps and a runner", model.Job{Steps: []model.Step{step}, RunnerName: &runner}, model.Ran),
	)
})
