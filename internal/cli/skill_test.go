package cli_test

import (
	"bytes"
	"slices"
	"strings"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/testsupport/doctest"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
	skill "github.com/rosenhouse/lg/skill/lg"
)

var _ = Describe("lg skill install", Label("skill"), func() {
	It("exits 1 with the error when it cannot write SKILL.md", func() {
		fsys := faultfs.New()
		fsys.FailOn("create", syscall.EROFS)
		var stderr bytes.Buffer

		code := cli.Main([]string{"skill", "install"}, cli.Deps{
			Env:     map[string]string{"CLAUDE_CONFIG_DIR": GinkgoT().TempDir()},
			Stdout:  &bytes.Buffer{},
			Stderr:  &stderr,
			Clock:   clock.Real{},
			StoreFS: fsys,
		})

		Expect(code).To(Equal(1))
		Expect(stderr.String()).To(ContainSubstring("read-only file system"))
	})
})

var _ = Describe("SKILL.md", Label("skill"), func() {
	It("uses only commands and flags that the lg Kong parser accepts in every `lg …` line", func() {
		commands := doctest.LgCommands(skill.Markdown)
		Expect(commands).NotTo(BeEmpty())
		for _, c := range commands {
			Expect(cli.Parse(c.Args)).To(Succeed(), "line %d: lg %s", c.Line, strings.Join(c.Args, " "))
		}
	})

	It("passes every flag its prose names in some `lg …` line", func() {
		commands := doctest.LgCommands(skill.Markdown)
		for _, span := range doctest.FlagSpans(skill.Markdown) {
			Expect(slices.ContainsFunc(commands, func(c doctest.Command) bool { return containsRun(c.Args, span.Args) })).
				To(BeTrue(), "line %d: %s", span.Line, strings.Join(span.Args, " "))
		}
	})
})

// containsRun reports whether words appear in args, in order and side by side.
func containsRun(args, words []string) bool {
	for i := range args {
		if slices.Equal(args[i:min(i+len(words), len(args))], words) {
			return true
		}
	}
	return false
}

var _ = Describe("Parse", Label("skill"), func() {
	DescribeTable("accepts every documented command line, without running it",
		func(line string) {
			Expect(cli.Parse(strings.Fields(line))).To(Succeed())
		},
		Entry(nil, "init --repo O/R --host H"),
		Entry(nil, "root"),
		Entry(nil, "version"),
		Entry(nil, "status --json"),
		Entry(nil, "sync --wait --timeout 1m"),
		Entry(nil, "daemon run"),
		Entry(nil, "daemon install"),
		Entry(nil, "daemon uninstall"),
		Entry(nil, "gc --dry-run --timeout 1m"),
		Entry(nil, "index rebuild"),
		Entry(nil, "paths --branch B --branch C --sha S --pr 1 --workflow W --job J --event E --conclusion C --since 30d --until 2026-09-01 --unit attempt -0"),
		Entry(nil, "where PATH HIT"),
		Entry(nil, "flakes --kind rerun --sha S --json"),
		Entry(nil, "extract --branch B --max-bytes 1GB --timeout 1m"),
		Entry(nil, "extract --all"),
		Entry(nil, "skill install"),
	)

	It("rejects an unknown flag", func() {
		Expect(cli.Parse([]string{"paths", "--branches", "main"})).To(MatchError(ContainSubstring("unknown flag --branches")))
	})
})
