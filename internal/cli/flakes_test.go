package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	It("prints rerun flips and intermittent failures without --kind, and each kind alone with --kind", func() {
		c := harness.NewCLI()
		for _, r := range scenario.Intermittent().All() {
			Expect(c.Fake.AddRun(r)).To(Succeed())
		}
		Expect(c.Main("sync")).To(Equal(0), c.Stderr.String())
		rerun := `run 3 (sha 3333333): "integration": 1:failure 2:success`
		intermittent := `workflow "lg-fixture" on main: "integration": 1 of 6 runs failed alone: run 3 (sha 3333333) failure`
		printed := func(args ...string) []string {
			GinkgoHelper()
			Expect(c.Main(append([]string{"flakes"}, args...)...)).To(Equal(0), c.Stderr.String())
			return strings.Split(strings.TrimSuffix(c.Stdout.String(), "\n"), "\n")
		}

		Expect(printed()).To(ContainElements(rerun, intermittent))
		Expect(printed("--kind", "rerun")).To(SatisfyAll(ContainElement(rerun), HaveEach(HavePrefix("run "))))
		Expect(printed("--kind", "intermittent")).To(SatisfyAll(ContainElement(intermittent), HaveEach(HavePrefix("workflow "))))
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

	It("needs status.json only for intermittent failures without --branch, and prints the rerun flips before naming --branch when it cannot read it", func() {
		c := harness.NewCLI()
		statusJSON := filepath.Join(c.Home, "state", "status.json")
		Expect(os.MkdirAll(filepath.Dir(statusJSON), 0o755)).To(Succeed())
		Expect(os.WriteFile(statusJSON, []byte("{"), 0o644)).To(Succeed())
		Expect(c.Main("flakes")).To(Equal(0), "with no data/, there is nothing to report: %s", c.Stderr.String())

		for _, r := range scenario.Intermittent().All() {
			Expect(c.Fake.AddRun(r)).To(Succeed())
		}
		Expect(c.Main("sync")).To(Equal(0), c.Stderr.String())
		Expect(os.WriteFile(statusJSON, []byte("{"), 0o644)).To(Succeed())

		Expect(c.Main("flakes", "--kind", "rerun")).To(Equal(0), c.Stderr.String())
		Expect(c.Main("flakes", "--kind", "intermittent", "--branch", "main")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(ContainSubstring(`"integration"`))
		Expect(c.Main("flakes")).To(Equal(1))
		Expect(c.Stdout.String()).To(SatisfyAll(ContainSubstring(`run 3 (sha 3333333): "integration"`), Not(ContainSubstring("failed alone"))))
		Expect(c.Stderr.String()).To(SatisfyAll(ContainSubstring(statusJSON), ContainSubstring("pass --branch")))
	})

	It("prints nothing before a sync puts anything in data/, though status.json records no default branch", func() {
		c := harness.NewCLI()
		c.WriteStatus(`{"repos": {"rosenhouse/lg": {"default_branch": ""}}}`)

		Expect(c.Main("flakes")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(BeEmpty())
	})

	It("names --branch when status.json records no default branch", func() {
		c := harness.NewCLI()
		for _, r := range scenario.Intermittent().All() {
			Expect(c.Fake.AddRun(r)).To(Succeed())
		}
		Expect(c.Main("sync")).To(Equal(0), c.Stderr.String())
		Expect(os.WriteFile(c.StatusFile(), []byte(`{"repos": {"rosenhouse/lg": {"default_branch": ""}}}`), 0o644)).To(Succeed())

		Expect(c.Main("flakes", "--kind", "intermittent")).To(Equal(1))
		Expect(c.Stderr.String()).To(ContainSubstring("pass --branch"))
	})

	It("prints an intermittent failure with no logs with an empty list in --json", func() {
		var out bytes.Buffer
		failures := []model.RunOutcome{{RunID: 2, Conclusion: "failure"}}
		Expect(cli.PrintIntermittentJSON(&out, index.Intermittent{Series: model.Series{Job: "test"}, Failures: failures})).To(Succeed())

		Expect(out.String()).To(ContainSubstring(`"logs":[]`))
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

	It("says in --help that its filters select runs and job names, not units", func() {
		c := harness.NewCLI()

		Expect(c.Main("flakes", "--help")).To(Equal(0))
		Expect(c.Stdout.String()).To(ContainSubstring("The other filters select runs"))
		Expect(c.Stdout.String()).NotTo(ContainSubstring("unit"))
	})

	DescribeTable("exits 2",
		func(args []string, message string) {
			c := harness.NewCLI()

			Expect(c.Main(append([]string{"flakes"}, args...)...)).To(Equal(2))
			Expect(c.Stdout.String()).To(BeEmpty())
			Expect(c.Stderr.String()).To(ContainSubstring(message))
		},
		Entry("for an empty --sha, which would match every run", []string{"--sha", ""}, "--sha must not be empty"),
		Entry("for an unknown --kind", []string{"--kind", "bogus"}, `--kind must be one of "rerun","intermittent","all" but got "bogus"`),
	)
})
