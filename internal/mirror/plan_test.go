package mirror_test

import (
	"runtime"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/model"
)

func listed(status string, runAttempt int) github.Run {
	return github.Run{Run: model.Run{Status: status, RunAttempt: runAttempt}}
}

var _ = Describe("Plan", Label("attempts"), func() {
	DescribeTable("yields the completed attempts not on disk, oldest first",
		func(run github.Run, onDisk []int, planned []int) {
			Expect(slices.Collect(mirror.Plan(run, onDisk))).To(Equal(planned))
		},
		Entry("run_attempt 3 with attempt-1 on disk", listed("completed", 3), []int{1}, []int{2, 3}),
		Entry("a complete run", listed("completed", 3), []int{1, 2, 3}, []int(nil)),
		Entry("a new run", listed("completed", 1), []int(nil), []int{1}),
		Entry("a gap", listed("completed", 3), []int{2}, []int{1, 3}),
		Entry("an in-progress latest attempt", listed("in_progress", 3), []int{1}, []int{2}),
		Entry("a run listed without run_attempt", listed("completed", 0), []int(nil), []int{1}),
	)

	It("plans no further than its caller asks, whatever run_attempt says", func() {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for n := range mirror.Plan(listed("completed", 1<<24), nil) {
			if n == 2 {
				break
			}
		}
		runtime.ReadMemStats(&after)

		Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", 1<<20))
	})
})
