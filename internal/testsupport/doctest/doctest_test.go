package doctest_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/doctest"
)

var _ = Describe("ShBlocks", Label("skill"), func() {
	It("finds sh blocks and ignores the others", func() {
		md := "# Title\n" +
			"```sh\nlg status\nlg sync --wait\n```\n" +
			"text\n" +
			"```json\n{\"sh\": 1}\n```\n" +
			"```\nuntagged\n```\n" +
			"```shell\nnot sh\n```\n" +
			"  ```sh\nindented\n  ```\n"
		Expect(doctest.ShBlocks(md)).To(Equal([]doctest.Block{
			{Line: 2, Text: "lg status\nlg sync --wait\n"},
			{Line: 16, Text: "indented\n"},
		}))
	})
})

var _ = Describe("LgCommands", Label("skill"), func() {
	It("finds each lg invocation in sh blocks and inline code, with its arguments unquoted, reading an unclosed quote or substitution to the end of the line", func() {
		md := "Run `lg status` first, and `lg sync --wait`. Not `rg lg` or `lg`.\n" +
			"```sh\n" +
			"lg paths --branch main -0 | xargs -0 rg -n 'foo bar'  # lg here is a comment\n" +
			"cd \"$(lg root)\" && lg paths --job \"build (ubuntu)\" --unit log; lg flakes\n" +
			"sha=$(lg paths --unit attempt | head -1) || lg status 2>/dev/null\n" +
			"if ! lg where x; then echo; fi\n" +
			"lg paths --sha abc \\\n" +
			"  --unit attempt\n" +
			"lg paths --job 'a b' --pr 42 | jq .\n" +
			"lg where 'unclosed\n" +
			"lg where \"unclosed\n" +
			"lg paths --pr 42|jq .\n" +
			"lg paths --job \"a \\\"b\\\"\" --job x\\ y\n" +
			"n=$(lg flakes --json | jq '(.run_id)')\n" +
			"echo $(lg root\n" +
			"```\n" +
			"```json\n" +
			"lg version\n" +
			"```\n"
		Expect(doctest.LgCommands(md)).To(Equal([]doctest.Command{
			{Line: 1, Args: []string{"status"}},
			{Line: 1, Args: []string{"sync", "--wait"}},
			{Line: 3, Args: []string{"paths", "--branch", "main", "-0"}},
			{Line: 4, Args: []string{"root"}},
			{Line: 4, Args: []string{"paths", "--job", "build (ubuntu)", "--unit", "log"}},
			{Line: 4, Args: []string{"flakes"}},
			{Line: 5, Args: []string{"paths", "--unit", "attempt"}},
			{Line: 5, Args: []string{"status"}},
			{Line: 6, Args: []string{"where", "x"}},
			{Line: 7, Args: []string{"paths", "--sha", "abc", "--unit", "attempt"}},
			{Line: 9, Args: []string{"paths", "--job", "a b", "--pr", "42"}},
			{Line: 10, Args: []string{"where", "unclosed"}},
			{Line: 11, Args: []string{"where", "unclosed"}},
			{Line: 12, Args: []string{"paths", "--pr", "42"}},
			{Line: 13, Args: []string{"paths", "--job", `a "b"`, "--job", "x y"}},
			{Line: 14, Args: []string{"flakes", "--json"}},
			{Line: 15, Args: []string{"root"}},
		}))
	})
})

var _ = Describe("FlagSpans", Label("skill"), func() {
	It("finds each inline code span that starts with --, split into words", func() {
		md := "Pass `--sha` (a prefix) or `--unit run`, not `lg paths --pr 1` or `-0`.\n" +
			"```sh\n" +
			"echo `--in-block`\n" +
			"```\n" +
			"Then `--all`.\n"
		Expect(doctest.FlagSpans(md)).To(Equal([]doctest.Command{
			{Line: 1, Args: []string{"--sha"}},
			{Line: 1, Args: []string{"--unit", "run"}},
			{Line: 5, Args: []string{"--all"}},
		}))
	})
})
