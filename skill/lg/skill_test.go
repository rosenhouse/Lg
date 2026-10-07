package skill_test

import (
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"

	skill "github.com/rosenhouse/lg/skill/lg"
)

var _ = Describe("SKILL.md", Label("skill"), func() {
	It("has name and description frontmatter", func() {
		rest, ok := strings.CutPrefix(skill.Markdown, "---\n")
		Expect(ok).To(BeTrue(), "SKILL.md must start with ---")
		front, _, ok := strings.Cut(rest, "\n---\n")
		Expect(ok).To(BeTrue(), "frontmatter must end with ---")

		var fields map[string]string
		Expect(yaml.Unmarshal([]byte(front), &fields)).To(Succeed())
		Expect(fields).To(HaveKeyWithValue("name", "lg"))
		Expect(fields).To(HaveKeyWithValue("description", Not(BeEmpty())))
		Expect(len(fields["description"])).To(BeNumerically("<=", 1024))
	})
})

var _ = DescribeTable("SKILL.md covers", Label("skill"),
	func(patterns ...string) {
		for _, p := range patterns {
			Expect(skill.Markdown).To(MatchRegexp(`(?i)`+p), p)
		}
	},
	Entry("lg status first, and lg sync --wait before concluding no match",
		"run `lg status` first", "`lg sync --wait`[^\n]*before[^\n]*no match"),
	Entry("exit codes",
		`\| 0 \| ok`, `\| 1 \| [^\n]*pending`, `\| 2 \| usage`, `\| 3 \| blocked`, `\| 4 \| timeout`),
	Entry("finding a carried-forward job's log with lg where",
		`carried[ -]forward[^\n]*lg where`, `original_log`),
	Entry("artifacts.json as a per-attempt snapshot",
		"`artifacts.json`[^\n]*snapshot"),
	Entry("re-indented JSON",
		`re-indented`, regexp.QuoteMeta(`'"head_sha": "`)),
	Entry("the BOM and timestamp prefix on log lines",
		`BOM`, `timestamp prefix`),
	Entry("continue-on-error failures reporting success",
		"`continue-on-error`[^\n]*success"),
	Entry("grep exit 1 and xargs exit 123 meaning no match",
		`exits? 1[^\n]*no match`, `123[^\n]*no match`),
	Entry("installing lg with go install, lg init and lg daemon install",
		`go install github\.com/rosenhouse/lg/cmd/lg@latest`, `lg init --repo`, `lg daemon install`),
	Entry("what each --unit prints",
		"`--unit run`", "`--unit attempt`", "`--unit job`", "`--unit log`", "`--unit artifact`", "`--unit extracted`"),
	Entry("W3 with lg paths --sha or --pr --unit attempt and jq",
		`lg paths --sha \S+ --unit attempt[^\n]*\|[^\n]*jq`, `lg paths --pr \d+ --unit attempt[^\n]*\|[^\n]*jq`),
	Entry("--pr missing fork pull_request runs, which --event and --sha find",
		"`--pr`[^\n]*fork[^\n]*`--event[^\n]*`--sha`"),
	Entry("flakes per job and per step",
		`lg flakes`, `per job[^\n]*per[^\n]*step`),
)
