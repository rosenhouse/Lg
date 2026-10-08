package cli_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing/iotest"

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

	It("exits 1 naming the error when --json cannot describe a hit", func() {
		Expect(c.Main("grep", "--json", "LG_MARKER flaky failure attempt=1")).To(Equal(0), c.Stderr.String())
		Expect(os.WriteFile(filepath.Join(filepath.Dir(flaky), "job.json"), []byte("{"), 0o644)).To(Succeed())

		Expect(c.Main("grep", "--json", "LG_MARKER flaky failure attempt=1")).To(Equal(1))
		Expect(c.Stderr.String()).To(ContainSubstring("job.json"))
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
	)
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

		Expect(err).To(HaveOccurred())
		Expect(stdout.String()).To(Equal("/a:1:foo\n/d:2:foo 4\n"))
		Expect(stderr.String()).To(Equal("lg: read /b: input/output error\nlg: open /c: permission denied\n"))
	})
})
