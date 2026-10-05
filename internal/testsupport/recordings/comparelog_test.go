package recordings_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = Describe("CompareLog", Label("sync"), func() {
	var jobDir string

	BeforeEach(func() {
		jobDir = GinkgoT().TempDir()
	})

	write := func(name, body string) {
		GinkgoHelper()
		Expect(os.WriteFile(filepath.Join(jobDir, name), []byte(body), 0o644)).To(Succeed())
	}

	It("accepts a log.txt identical to the recording", func() {
		write("log.txt", "\xef\xbb\xbfline\n")
		Expect(recordings.CompareLog([]byte("\xef\xbb\xbfline\n"), jobDir)).To(Succeed())
	})

	It("rejects a log.txt that differs, naming it", func() {
		write("log.txt", "\xef\xbb\xbfline\n")
		Expect(recordings.CompareLog([]byte("line\n"), jobDir)).To(MatchError(ContainSubstring(filepath.Join(jobDir, "log.txt"))))
	})

	DescribeTable("accepts a tombstone whose reason says GitHub no longer serves the log",
		func(reason string) {
			write("log.txt.tombstone", `{"target":"log.txt","reason":"`+reason+`"}`)
			Expect(recordings.CompareLog([]byte("line\n"), jobDir)).To(Succeed())
		},
		Entry("expired", "expired"),
		Entry("deleted", "deleted"),
	)

	DescribeTable("rejects any other tombstone, naming its reason",
		func(reason string) {
			write("log.txt.tombstone", `{"target":"log.txt","reason":"`+reason+`"}`)
			Expect(recordings.CompareLog([]byte("line\n"), jobDir)).To(MatchError(ContainSubstring(reason)))
		},
		Entry("not_applicable", "not_applicable"),
		Entry("too_large", "too_large"),
	)

	It("rejects a job dir with neither a log nor a tombstone", func() {
		Expect(recordings.CompareLog([]byte("line\n"), jobDir)).To(MatchError(ContainSubstring("log.txt.tombstone")))
	})
})
