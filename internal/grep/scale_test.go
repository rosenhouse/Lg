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

// logLines gives about mb megabytes of log lines. Every tenth is an error,
// and every 500th a lone "!" or a rare line.
func logLines(mb int) string {
	var b strings.Builder
	for n := 0; b.Len() < mb<<20; n++ {
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
		content := logLines(mb)
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
	Entry("a class that would match newlines up to a later line", `\d[^@]*[!?]`, 4, 2*time.Second),
	Entry("a (?s) dot that would match newlines up to a later line", `(?s)\d.*[!?]`, 4, 2*time.Second),
)
