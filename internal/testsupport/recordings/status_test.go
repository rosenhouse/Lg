package recordings_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = Describe("ParseStatus", Label("transport"), func() {
	It("reads a status and path per line, with a redirect's first-hop and final status", func() {
		lines, err := recordings.ParseStatus(strings.NewReader(
			"200 runs/1/jobs?filter=all&per_page=100\n302->404 jobs/2/logs\n404->404 runs/1/attempts/1/logs\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(lines).To(Equal([]recordings.Line{
			{First: 200, Final: 200, Path: "runs/1/jobs?filter=all&per_page=100"},
			{First: 302, Final: 404, Path: "jobs/2/logs"},
			{First: 404, Final: 404, Path: "runs/1/attempts/1/logs"},
		}))
	})

	DescribeTable("rejects a malformed line, naming it",
		func(line string) {
			_, err := recordings.ParseStatus(strings.NewReader("200 runs/1\n" + line + "\n"))
			Expect(err).To(MatchError(ContainSubstring("line 2: %q", line)))
		},
		Entry("no path", "200"),
		Entry("no status", "runs/1"),
		Entry("a bad final status", "302->x jobs/2/logs"),
	)
})
