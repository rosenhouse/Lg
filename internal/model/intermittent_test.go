package model_test

import (
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
