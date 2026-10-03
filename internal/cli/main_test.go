package cli_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
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
		Expect(run(map[string]string{}, "root")).To(Equal(1))
		Expect(stderr.String()).To(Equal("lg: HOME is not set\n"))
		Expect(stdout.String()).To(BeEmpty())
	})

	It("exits 0 and prints usage for --help", func() {
		Expect(run(map[string]string{}, "--help")).To(Equal(0))
		Expect(stdout.String()).To(HavePrefix("Usage: lg <command>"))
	})

	It("exits 2 when no command is given", func() {
		Expect(run(map[string]string{})).To(Equal(2))
		Expect(stderr.String()).To(ContainSubstring("Usage: lg <command>"))
	})
})
