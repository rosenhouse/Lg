package cli_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing/iotest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg grep", Label("grep"), func() {
	var (
		c     *harness.CLI
		flaky string
	)

	BeforeEach(func() {
		c = harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))
		run := filepath.Join(c.Home, "data", "github.com", "rosenhouse", "Lg", "runs", "2026-10-03", "37129390741_lg-fixture_lg-fixture")
		flaky = filepath.Join(run, "attempt-1", "jobs", "111221289888_flaky", "log.txt")
		Expect(flaky).To(BeARegularFile())
	})

	It("matches case-insensitively with -i", func() {
		Expect(c.Main("grep", "-l", "lg_marker FLAKY failure")).To(Equal(5))
		Expect(c.Main("grep", "-l", "-i", "lg_marker FLAKY failure")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(Equal(flaky + "\n"))
	})

	It("matches PATTERN as a literal string with -F", func() {
		literal := `"LG_MARKER flaky report attempt=$ATTEMPT" > report.txt`
		Expect(c.Main("grep", "-l", literal)).To(Equal(5))
		Expect(c.Main("grep", "-F", literal)).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(HavePrefix(flaky + ":32:"))
	})

	It("searches the files of --unit, such as each job.json", func() {
		Expect(c.Main("grep", "--unit", "job", "-l", `"name": "flaky"`)).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(Equal(filepath.Join(filepath.Dir(flaky), "job.json") + "\n"))
	})

	It("names a file that --json cannot describe and the runs it cannot read, prints the other hits, and exits 1", func() {
		Expect(c.Main("grep", "--json", "LG_MARKER")).To(Equal(0), c.Stderr.String())
		hits := strings.Count(c.Stdout.String(), "\n")
		inFlaky := strings.Count(c.Stdout.String(), `"job":"flaky"`)
		Expect(inFlaky).To(BeNumerically(">", 1))
		Expect(os.WriteFile(filepath.Join(filepath.Dir(flaky), "job.json"), []byte("{"), 0o644)).To(Succeed())
		run := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(flaky))))
		copied := filepath.Join(filepath.Dir(run), "99_lg-fixture_lg-fixture")
		Expect(os.CopyFS(copied, os.DirFS(run))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(copied, "attempt-1", "attempt.json"), []byte("{"), 0o644)).To(Succeed())

		Expect(c.Main("grep", "--json", "LG_MARKER")).To(Equal(1))
		Expect(strings.Count(c.Stdout.String(), "\n")).To(Equal(hits - inFlaky))
		Expect(c.Stdout.String()).NotTo(ContainSubstring(`"job":"flaky"`))
		Expect(c.Stderr.String()).To(SatisfyAll(
			ContainSubstring(filepath.Join(copied, "attempt-1", "attempt.json")),
			ContainSubstring(filepath.Join(filepath.Dir(flaky), "job.json")),
		))
	})

	It("prints hit text in JSON as is, without escaping HTML", func() {
		Expect(os.WriteFile(flaky, []byte("x <a & b>\n"), 0o644)).To(Succeed())

		Expect(c.Main("grep", "--json", "<a & b>")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(ContainSubstring(`"text":"x <a & b>"`))
	})

	It("separates the paths of -l with NUL with -0", func() {
		Expect(c.Main("grep", "-l", "-0", "LG_MARKER flaky failure")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(Equal(flaky + "\x00"))
	})

	It("names a file whose path holds a newline instead of printing its hits as text, which --json and -l -0 print", func() {
		extracted := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(flaky)))), "artifacts", "11276401837_flaky-report", "extracted")
		Expect(os.MkdirAll(extracted, 0o755)).To(Succeed())
		odd := filepath.Join(extracted, "new\nline.txt")
		Expect(os.WriteFile(odd, []byte("needle\n"), 0o644)).To(Succeed())

		for _, args := range [][]string{{"needle"}, {"-l", "needle"}} {
			Expect(c.Main(append([]string{"grep"}, args...)...)).To(Equal(1), "%v", args)
			Expect(c.Stdout.String()).To(BeEmpty())
			Expect(c.Stderr.String()).To(Equal("lg: " + strconv.Quote(odd) + " holds a newline; use --json, or -l -0\n"))
		}
		Expect(c.Main("grep", "-l", "-0", "needle")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(Equal(odd + "\x00"))
		Expect(c.Main("grep", "--json", "needle")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(ContainSubstring(`"inner_path":"new\nline.txt"`))
	})

	It("takes a pattern that starts with - after --", func() {
		Expect(os.WriteFile(flaky, []byte("--- FAIL: TestFoo\n"), 0o644)).To(Succeed())

		Expect(c.Main("grep", "--", "--- FAIL")).To(Equal(0), c.Stderr.String())
		Expect(c.Stdout.String()).To(Equal(flaky + ":1:--- FAIL: TestFoo\n"))
	})

	It("exits 5 saying how many files it searched, in the singular for one", func() {
		Expect(c.Main("grep", "--job", "flaky", "no such text")).To(Equal(5))
		Expect(c.Stdout.String()).To(BeEmpty())
		Expect(c.Stderr.String()).To(Equal("lg: no match in 1 file\n"))
	})

	DescribeTable("exits 2 for",
		func(args []string, message string) {
			Expect(c.Main(append([]string{"grep"}, args...)...)).To(Equal(2))
			Expect(c.Stderr.String()).To(ContainSubstring(message))
		},
		Entry("an invalid regular expression", []string{"foo("}, "missing closing )"),
		Entry("--unit artifact, whose zips it cannot search", []string{"--unit", "artifact", "foo"}, "--unit"),
		Entry("--unit given twice", []string{"--unit", "log", "--unit", "job", "foo"}, "--unit must not be given more than once"),
		Entry("an empty filter", []string{"--branch", "", "foo"}, "--branch must not be empty"),
		Entry("a pattern that matches a newline", []string{`a\nb`}, "the pattern matches a newline"),
		Entry("-0 without -l", []string{"-0", "foo"}, "-0 needs -l, without --json"),
		Entry("-0 with --json", []string{"-l", "-0", "--json", "foo"}, "-0 needs -l, without --json"),
	)

	It("says once that it cannot write stdout", func() {
		var stderr bytes.Buffer
		code := cli.Main([]string{"grep", "LG_MARKER"}, cli.Deps{
			Env:    map[string]string{"LG_HOME": c.Home, "LG_CONFIG": c.Config, "LG_TEST_NOW": harness.DefaultNow().Format(time.RFC3339)},
			Stdout: failingWriter{errDiskFull}, Stderr: &stderr, Clock: clock.Real{},
		})
		Expect(code).To(Equal(1))
		Expect(stderr.String()).To(Equal("lg: disk full\n"))
	})
})

var _ = Describe("lg grep", Label("grep"), func() {
	It("exits 5 saying it searched 0 files before the first sync", func() {
		var stdout, stderr bytes.Buffer
		code := cli.Main([]string{"grep", "foo"}, cli.Deps{Env: map[string]string{"LG_HOME": GinkgoT().TempDir()}, Stdout: &stdout, Stderr: &stderr, Clock: clock.Real{}})
		Expect(code).To(Equal(5))
		Expect(stdout.String()).To(BeEmpty())
		Expect(stderr.String()).To(Equal(neverSynced + "lg: no match in 0 files\n"))
	})

	It("names each file it cannot open or read, and searches the others", func() {
		bodies := map[string]io.Reader{
			"/a": strings.NewReader("foo\n"),
			"/b": io.MultiReader(strings.NewReader("foo 2\n"), iotest.ErrReader(errors.New("read /b: input/output error"))),
			"/d": strings.NewReader("bar\nfoo 4\n"),
		}
		open := func(path string) (io.ReadCloser, error) {
			body, ok := bodies[path]
			if !ok {
				return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EACCES}
			}
			return io.NopCloser(body), nil
		}
		var stdout, stderr bytes.Buffer

		err := cli.GrepFiles(&stdout, &stderr, open, "foo", "/a", "/b", "/c", "/d")

		Expect(errors.As(err, new(cli.Warned))).To(BeTrue(), "already printed")
		Expect(stdout.String()).To(Equal("/a:1:foo\n/b:1:foo 2\n/d:2:foo 4\n"))
		Expect(stderr.String()).To(Equal("lg: read /b: input/output error\nlg: open /c: permission denied\n"))
	})
})

var _ = Describe("lg grep --json", Label("scale"), func() {
	It("prints 50,000 hits in one file in under 1s", func() {
		c := harness.NewCLI()
		Expect(c.Main("sync")).To(Equal(0))
		log := filepath.Join(c.Home, "data", "github.com", "rosenhouse", "Lg", "runs", "2026-10-03", "37129390741_lg-fixture_lg-fixture", "attempt-1", "jobs", "111221289888_flaky", "log.txt")
		Expect(os.WriteFile(log, []byte(strings.Repeat("needle\n", 50_000)), 0o644)).To(Succeed())

		start := clock.Real{}.Now()
		Expect(c.Main("grep", "--json", "needle")).To(Equal(0), c.Stderr.String())
		took := clock.Real{}.Now().Sub(start)
		AddReportEntry("timings", took.String())
		Expect(took).To(BeNumerically("<", time.Second))
	})
})
