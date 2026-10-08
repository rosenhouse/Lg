package grep_test

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/grep"
)

// logLines gives about size bytes of log lines. Every tenth is an error, and
// every 500th a lone "!" or a rare line.
func logLines(size int) string {
	var b strings.Builder
	for n := 0; b.Len() < size; n++ {
		fmt.Fprintf(&b, "2026-10-03T14:22:57.%07dZ step %d ran in %d ms\n", n%10_000_000, n%97, n%1000)
		switch {
		case n%10 == 0:
			b.WriteString("2026-10-03T14:22:57.0000000Z ##[error]Process completed with exit code 1.\n")
		case n%1000 == 499:
			b.WriteString("!\n")
		case n%1000 == 999:
			b.WriteString("rare\n")
		}
	}
	return b.String()
}

var _ = DescribeTable("Lines searches log lines in time, for", Label("scale"),
	func(pattern string, mb int, limit time.Duration) {
		content := logLines(mb << 20)
		m, err := grep.Compile(pattern)
		Expect(err).NotTo(HaveOccurred())
		hits := 0
		start := clock.Real{}.Now()
		Expect(m.Lines(strings.NewReader(content), func(int, []byte) bool {
			hits++
			return true
		})).To(Succeed())
		took := clock.Real{}.Now().Sub(start)
		AddReportEntry("timings", fmt.Sprintf("%s over %d MB: %d hits in %s", pattern, mb, hits, took))
		Expect(took).To(BeNumerically("<", limit))
	},
	Entry("a literal", "no such text", 64, time.Second),
	Entry("a case-insensitive literal", "(?i)no such text", 64, time.Second),
	Entry("an alternation of a frequent literal and an absent one", "(?i)error|no such text", 64, 2*time.Second),
	Entry("an alternation of a literal on most lines and a rare one", "(?i) ms$|rare", 16, 2*time.Second),
)

var _ = Describe("Lines", Label("scale"), func() {
	It("searches 20,000 small files in under 1s", func() {
		m, err := grep.Compile("needle")
		Expect(err).NotTo(HaveOccurred())
		content := logLines(2 << 10)
		start := clock.Real{}.Now()
		for range 20_000 {
			Expect(m.Lines(strings.NewReader(content), func(int, []byte) bool { return true })).To(Succeed())
		}
		took := clock.Real{}.Now().Sub(start)
		AddReportEntry("timings", fmt.Sprintf("20,000 small files in %s", took))
		Expect(took).To(BeNumerically("<", time.Second))
	})
})
