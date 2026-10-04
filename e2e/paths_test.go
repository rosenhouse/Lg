package e2e_test

import (
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg paths", Label("sync"), func() {
	var (
		env  *harness.Env
		logs []string
	)

	BeforeEach(func() {
		env = harness.New(lgPath)
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())
		Expect(env.Sync()).To(gexec.Exit(0))

		var err error
		logs, err = filepath.Glob(filepath.Join(env.Data(), "*/*/*/runs/*/*/attempt-*/jobs/*/log.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(logs).To(HaveLen(10))
	})

	It("prints the absolute path of each mirrored log.txt, one per line, and NUL-separated with -0", func() {
		lines := env.Lg("paths")
		Eventually(lines, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(lines.Out.Contents())).To(HaveSuffix("\n"))
		Expect(strings.Split(strings.TrimSuffix(string(lines.Out.Contents()), "\n"), "\n")).To(ConsistOf(logs))

		nul := env.Lg("paths", "-0")
		Eventually(nul, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(nul.Out.Contents())).To(HaveSuffix("\x00"))
		Expect(strings.Split(strings.TrimSuffix(string(nul.Out.Contents()), "\x00"), "\x00")).To(ConsistOf(logs))
	})

	It("piped to `xargs -0 -r grep -l 'LG_MARKER flaky failure attempt=1'` prints only the log of job 111221289888", func() {
		grep := env.Sh("lg paths -0 | xargs -0 -r grep -l 'LG_MARKER flaky failure attempt=1'")
		Eventually(grep, harness.ExitTimeout).Should(gexec.Exit(0))
		flaky, err := filepath.Glob(filepath.Join(env.Data(), "*/*/*/runs/*/*/attempt-1/jobs/111221289888_*/log.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(flaky).To(HaveLen(1))
		Expect(string(grep.Out.Contents())).To(Equal(flaky[0] + "\n"))
	})
})
