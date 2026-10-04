package cli_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/failure"
)

var _ = Describe("Main", Label("cli"), func() {
	var stdout, stderr *bytes.Buffer

	BeforeEach(func() {
		stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	})

	run := func(env map[string]string, args ...string) int {
		return cli.Main(args, cli.Deps{Env: env, Stdout: stdout, Stderr: stderr})
	}

	It("exits 1 and prints the error when a command fails", func() {
		code := cli.Main([]string{"root"}, cli.Deps{Env: map[string]string{"LG_HOME": "/lg"}, Stdout: failingWriter{errDiskFull}, Stderr: stderr})
		Expect(code).To(Equal(1))
		Expect(stderr.String()).To(Equal("lg: disk full\n"))
	})

	It("exits 3 and prints the error when a command is blocked", Label("blocked"), func() {
		blocked := fmt.Errorf("sync: %w", failure.Blocked{Kind: failure.Auth, Detail: "401 Unauthorized"})
		code := cli.Main([]string{"root"}, cli.Deps{Env: map[string]string{"LG_HOME": "/lg"}, Stdout: failingWriter{blocked}, Stderr: stderr})
		Expect(code).To(Equal(3))
		Expect(stderr.String()).To(Equal("lg: sync: blocked (auth): 401 Unauthorized\n"))
	})

	It("prefixes every line of a multi-line error", func() {
		failing := failingWriter{errors.Join(errors.New("run 1: 502 Bad Gateway"), errors.New("run 2: 503 Service Unavailable"))}
		code := cli.Main([]string{"root"}, cli.Deps{Env: map[string]string{"LG_HOME": "/lg"}, Stdout: failing, Stderr: stderr})
		Expect(code).To(Equal(1))
		Expect(stderr.String()).To(Equal("lg: run 1: 502 Bad Gateway\nlg: run 2: 503 Service Unavailable\n"))
	})

	It("exits 2 and prints the error for a config error", func() {
		Expect(run(map[string]string{"LG_HOME": "rel"}, "root")).To(Equal(2))
		Expect(stderr.String()).To(Equal("lg: LG_HOME must be an absolute path: \"rel\"\n"))
		Expect(stdout.String()).To(BeEmpty())
	})

	It("prints the version even when it refuses the store", func() {
		home := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(home, "FORMAT"), []byte("lg-store 2\n"), 0o644)).To(Succeed())

		Expect(run(map[string]string{"LG_HOME": home}, "version")).To(Equal(0))
		Expect(run(map[string]string{"LG_HOME": home}, "root")).To(Equal(1))
	})

	It("exits 0 and prints usage for --help", func() {
		Expect(run(map[string]string{}, "--help")).To(Equal(0))
		Expect(stdout.String()).To(HavePrefix("Usage: lg <command>"))
	})

	It("exits 1 without usage when help cannot be written", func() {
		code := cli.Main([]string{"--help"}, cli.Deps{Env: map[string]string{}, Stdout: failingWriter{errDiskFull}, Stderr: stderr})
		Expect(code).To(Equal(1))
		Expect(stderr.String()).To(Equal("lg: disk full\n"))
	})

	It("exits 2 for an LG_TEST_NOW that is not RFC 3339", Label("failures"), func() {
		Expect(run(map[string]string{"LG_TEST_NOW": "yesterday"}, "version")).To(Equal(2))
		Expect(stderr.String()).To(HavePrefix(`lg: LG_TEST_NOW: parsing time "yesterday"`))
		Expect(stdout.String()).To(BeEmpty())
	})

	It("exits 2 when no command is given", func() {
		Expect(run(map[string]string{})).To(Equal(2))
		Expect(stderr.String()).To(HavePrefix("lg: expected one of \"root\", \"version\", \"sync\", \"paths\"\nUsage: lg <command>"))
	})
})

var errDiskFull = errors.New("disk full")

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }
