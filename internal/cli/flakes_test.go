package cli_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg flakes", Label("flakes"), func() {
	It("prints with --kind rerun what it prints without --kind", func() {
		c := harness.NewCLI()
		for _, stage := range []string{"after-attempt-2", "after-attempt-3"} {
			Expect(c.Fake.Advance(fixtureRun, stage)).To(Succeed())
			Expect(c.Main("sync")).To(Equal(0))
		}
		Expect(c.Main("flakes")).To(Equal(0), c.Stderr.String())
		all := c.Stdout.String()
		Expect(all).To(ContainSubstring(`"flaky"`))

		Expect(c.Main("flakes", "--kind", "rerun")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(Equal(all))
	})

	It("exits 2 for an empty --sha, which would match every run", func() {
		c := harness.NewCLI()

		Expect(c.Main("flakes", "--sha", "")).To(Equal(2))
		Expect(c.Stdout.String()).To(BeEmpty())
		Expect(c.Stderr.String()).To(ContainSubstring("--sha must not be empty"))
	})
})
