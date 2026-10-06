package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("lg flakes", Label("flakes"), func() {
	// synced syncs the fixture run through after-attempt-3, and gives its run dir.
	synced := func(c *harness.CLI) string {
		GinkgoHelper()
		Expect(c.Main("sync")).To(Equal(0))
		for _, stage := range []string{"after-attempt-2", "after-attempt-3"} {
			Expect(c.Fake.Advance(fixtureRun, stage)).To(Succeed())
			Expect(c.Main("sync")).To(Equal(0))
		}
		runDirs, err := filepath.Glob(filepath.Join(c.Home, "data", "*", "*", "*", "runs", "*", fmt.Sprintf("%d_*", fixtureRun)))
		Expect(err).NotTo(HaveOccurred())
		Expect(runDirs).To(HaveLen(1))
		return runDirs[0]
	}

	It("prints with --kind rerun what it prints without --kind", func() {
		c := harness.NewCLI()
		synced(c)
		Expect(c.Main("flakes")).To(Equal(0), c.Stderr.String())
		all := c.Stdout.String()
		Expect(all).To(ContainSubstring(`"flaky"`))

		Expect(c.Main("flakes", "--kind", "rerun")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(Equal(all))
	})

	It("prints the findings, then exits 1 naming each log it cannot read", func() {
		c := harness.NewCLI()
		spoiled := filepath.Join(synced(c), "attempt-2", "jobs", "111221661475_flaky")
		Expect(os.RemoveAll(spoiled)).To(Succeed())
		Expect(os.WriteFile(spoiled, nil, 0o644)).To(Succeed())

		Expect(c.Main("flakes")).To(Equal(1))
		Expect(c.Stdout.String()).To(ContainSubstring(`"flaky"`))
		Expect(c.Stderr.String()).To(ContainSubstring(spoiled))
	})

	It("prints a finding with no failing steps or logs with empty lists in --json", func() {
		var out bytes.Buffer
		Expect(cli.PrintFlipJSON(&out, index.Flip{Flip: model.Flip{RunID: 1, Job: "test"}})).To(Succeed())

		Expect(out.String()).To(SatisfyAll(ContainSubstring(`"failing_steps":[]`), ContainSubstring(`"logs":[]`)))
	})

	It("prints <, > and & in --json unescaped", func() {
		c := harness.NewCLI()
		r := scenario.Clone(scenario.Recorded(fixtureRun, "after-attempt-1"), 1)
		r = scenario.SetJobConclusion(r, 1, r.JobIDs(1, "pass")[0], "failure")
		Expect(c.Fake.AddRun(scenario.AddRerunAttempt(scenario.RenameJob(r, 1, "pass", "<a & b>"), "<a & b>"))).To(Succeed())
		Expect(c.Main("sync")).To(Equal(0))

		Expect(c.Main("flakes", "--json", "--job", "<a & b>")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(ContainSubstring(`"job":"<a & b>"`))
	})

	It("exits 1 when it cannot print", func() {
		c := harness.NewCLI()
		synced(c)
		var stderr bytes.Buffer

		code := cli.Main([]string{"flakes"}, cli.Deps{
			Env:    map[string]string{"LG_HOME": c.Home, "LG_CONFIG": c.Config},
			Stdout: failingWriter{syscall.ENOSPC},
			Stderr: &stderr,
			Clock:  clock.Real{},
		})
		Expect(code).To(Equal(1))
		Expect(stderr.String()).To(ContainSubstring(syscall.ENOSPC.Error()))
	})

	DescribeTable("exits 2",
		func(args []string, message string) {
			c := harness.NewCLI()

			Expect(c.Main(append([]string{"flakes"}, args...)...)).To(Equal(2))
			Expect(c.Stdout.String()).To(BeEmpty())
			Expect(c.Stderr.String()).To(ContainSubstring(message))
		},
		Entry("for an empty --sha, which would match every run", []string{"--sha", ""}, "--sha must not be empty"),
		Entry("for an unknown --kind", []string{"--kind", "bogus"}, `--kind must be one of "rerun","all" but got "bogus"`),
	)
})
