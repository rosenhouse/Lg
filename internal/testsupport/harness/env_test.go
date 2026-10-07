package harness_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("New", Label("skill"), func() {
	It("stops a -race lg from sleeping a second as it exits", func() {
		Expect(harness.New("/no/lg").Getenv("GORACE")).To(Equal("atexit_sleep_ms=0"))
	})
})

var _ = Describe("Env.Bash", Label("skill"), func() {
	DescribeTable("exits non-zero",
		func(script string) {
			session := harness.New("/no/lg").Bash(script)
			Eventually(session, harness.ExitTimeout).Should(gexec.Exit())
			Expect(session.ExitCode()).NotTo(Equal(0))
		},
		Entry("on a failing command before the last", "false; echo ok"),
		Entry("on a failing stage in a pipe", "false | cat"),
		Entry("on an unset variable", "echo $UNSET_VAR"),
	)

	It("runs in HOME", func() {
		env := harness.New("/no/lg")
		session := env.Bash("pwd -P")
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		home, err := filepath.EvalSymlinks(env.Home())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(session.Out.Contents())).To(Equal(home + "\n"))
	})
})

var _ = Describe("Env.BashUnchecked", Label("skill"), func() {
	It("runs on past a failing command and pipe stage, in HOME", func() {
		env := harness.New("/no/lg")
		session := env.BashUnchecked("false; false | cat; echo $UNSET_VAR ok; pwd -P")
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		home, err := filepath.EvalSymlinks(env.Home())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(session.Out.Contents())).To(Equal("ok\n" + home + "\n"))
	})
})
