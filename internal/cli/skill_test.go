package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/testsupport/doctest"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
	skill "github.com/rosenhouse/lg/skill/lg"
)

var _ = Describe("lg skill install", Label("skill"), func() {
	DescribeTable("exits 1 with the error when it cannot write SKILL.md",
		func(op string) {
			fsys := faultfs.New()
			fsys.FailOn(op, syscall.EROFS)
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
		},
		Entry("making its dir", "mkdir"),
		Entry("creating the file", "create"),
	)
})

var _ = DescribeTable("uses only commands and flags that the lg Kong parser accepts in every `lg …` line of", Label("skill"),
	func(markdown func() string) {
		md := markdown()
		commands := doctest.LgCommands(md)
		Expect(commands).NotTo(BeEmpty())
		var lines []int
		for _, c := range commands {
			Expect(cli.Parse(c.Args)).To(Succeed(), "line %d: lg %s", c.Line, strings.Join(c.Args, " "))
			lines = append(lines, c.Line)
		}
		Expect(lines).To(Equal(doctest.LgMentions(md)), "each mention of lg must be an invocation that LgCommands finds")
	},
	Entry("SKILL.md", func() string { return skill.Markdown }),
	Entry("README.md", readme),
)

var _ = DescribeTable("passes every flag its prose names in some `lg …` line of", Label("skill"),
	func(markdown func() string) {
		md := markdown()
		commands := doctest.LgCommands(md)
		for _, span := range doctest.FlagSpans(md) {
			Expect(slices.ContainsFunc(commands, func(c doctest.Command) bool { return containsRun(c.Args, span.Args) })).
				To(BeTrue(), "line %d: %s", span.Line, strings.Join(span.Args, " "))
		}
	},
	Entry("SKILL.md", func() string { return skill.Markdown }),
	Entry("README.md", readme),
)

var _ = Describe("README.md", Label("skill"), func() {
	It("states the defaults that config.Defaults gives", func() {
		d := config.Defaults()
		for key, value := range map[string]fmt.Stringer{
			"sync_interval": d.SyncInterval, "backfill": d.Backfill, "retention": d.Retention, "disk_cap": d.DiskCap,
		} {
			Expect(readme()).To(ContainSubstring(fmt.Sprintf("`%s`, %s by default", key, value)))
		}
	})
})

func readme() string {
	GinkgoHelper()
	md, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	Expect(err).NotTo(HaveOccurred())
	return string(md)
}

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
