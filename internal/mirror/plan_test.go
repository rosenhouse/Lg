package mirror_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/mirror"
)

var _ = DescribeTable("PlanAttempts lists the attempts not on disk, oldest first", Label("attempts"),
	func(runAttempt int, onDisk []int, planned []int) {
		Expect(mirror.PlanAttempts(runAttempt, onDisk)).To(Equal(planned))
	},
	Entry("run_attempt 3 with attempt-1 on disk", 3, []int{1}, []int{2, 3}),
	Entry("a complete run", 3, []int{1, 2, 3}, []int(nil)),
	Entry("a new run", 1, []int(nil), []int{1}),
	Entry("a gap", 3, []int{2}, []int{1, 3}),
)
