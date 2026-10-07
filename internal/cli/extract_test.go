package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = DescribeTable("lg extract", Label("extract"),
	func(args []string, want time.Duration) {
		home := GinkgoT().TempDir()
		Expect(store.Init(home)).To(Succeed())
		writeLock := filepath.Join(home, "state", "write.lock")
		held, err := lock.Wait(writeLock, time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		var stderr bytes.Buffer
		clk := &firedClock{}

		code := cli.Main(append([]string{"extract"}, args...), cli.Deps{
			Env:    map[string]string{"LG_HOME": home},
			Stdout: &bytes.Buffer{},
			Stderr: &stderr,
			Clock:  clk,
		})
		Expect(code).To(Equal(4))
		Expect(clk.requested()).To(ContainElement(want))
		Expect(stderr.String()).To(HaveSuffix(fmt.Sprintf("lg: %s is held by pid %d; gave up after %s\n", writeLock, os.Getpid(), want)))
	},
	Entry("waits up to 5m for the write lock, then exits 4", []string{"--all"}, 5*time.Minute),
	Entry("waits up to --timeout for the write lock, then exits 4", []string{"--all", "--timeout", "1s"}, time.Second),
)

var _ = DescribeTable("lg extract exits 2", Label("extract"),
	func(args []string, message string) {
		var stderr bytes.Buffer
		code := cli.Main(append([]string{"extract"}, args...), cli.Deps{
			Env:    map[string]string{"LG_HOME": GinkgoT().TempDir()},
			Stdout: &bytes.Buffer{},
			Stderr: &stderr,
			Clock:  clock.Real{},
		})
		Expect(code).To(Equal(2))
		Expect(stderr.String()).To(ContainSubstring(message))
	},
	Entry("given --all and a filter", []string{"--all", "--branch", "main"}, "--all takes no filters or PATHs"),
	Entry("given --all and a PATH", []string{"--all", "x"}, "--all takes no filters or PATHs"),
	Entry("given a filter and a PATH", []string{"--branch", "main", "x"}, "give filters or PATHs, not both"),
	Entry("given a negative --timeout", []string{"--all", "--timeout=-1s"}, "--timeout must not be negative"),
	Entry("given a --max-bytes that is not a size", []string{"--all", "--max-bytes", "1 GB"}, `want a size such as 500MB, not "1 GB"`),
	Entry("when LG_HOME holds no store", []string{"--all"}, "holds no lg store; check LG_HOME"),
)

var _ = Describe("lg extract PATH", Label("extract"), func() {
	It("exits 1 naming each PATH that is not in an artifact dir, and extracts the others", func() {
		c := harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))
		run := filepath.Join(c.Home, "data", "github.com", "rosenhouse", "Lg", "runs", "2026-10-03", "37129390741_lg-fixture_lg-fixture")
		artifact := filepath.Join(run, "artifacts", "11276272069_pass-artifact")
		outside := GinkgoT().TempDir()

		Expect(c.Main("extract", filepath.Join(run, "attempt-1"), outside, filepath.Join(artifact, "artifact.zip"))).To(Equal(1))
		Expect(c.Stderr.String()).To(ContainSubstring(filepath.Join(run, "attempt-1") + " is not in an artifact dir"))
		Expect(c.Stderr.String()).To(ContainSubstring(outside + " is outside " + filepath.Join(c.Home, "data")))
		Expect(c.Stdout.String()).To(Equal(filepath.Join(artifact, "extracted") + "\n"))
	})
})
