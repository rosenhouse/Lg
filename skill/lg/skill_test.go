package skill_test

import (
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"

	"github.com/rosenhouse/lg/internal/testsupport/doctest"

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

	It("tags every fenced block sh or text, so that the doc-tests see every block of shell", func() {
		Expect(doctest.Tags(skill.Markdown)).To(HaveEach(BeElementOf("sh", "text")))
		Expect(skill.Markdown).NotTo(MatchRegexp("(?m)^ *~~~"))
	})

	It("never cds into a command substitution, whose failure bash ignores", func() {
		Expect(skill.Markdown).NotTo(MatchRegexp(`cd "?\$\(`))
	})
})

var _ = DescribeTable("SKILL.md covers", Label("skill"),
	func(patterns ...string) {
		for _, p := range patterns {
			Expect(skill.Markdown).To(MatchRegexp(`(?i)`+p), p)
		}
	},
	Entry("lg status first, and lg sync --wait before concluding no match",
		"run `lg status` first", "`lg sync --wait --timeout[^\n]*no match", `exit 4[^\n]*not finished`),
	Entry("exit codes",
		`\| 0 \| ok`, `\| 1 \| [^\n]*pending`, `\| 2 \| usage`, `\| 3 \| blocked`, `\| 4 \| timeout`, `\| 5 \| no match`),
	Entry("lg grep with filters, and with --json for lg where's fields",
		`lg grep --branch [^\n]*'foo bar'`, `lg grep [^\n]*--json[^\n]*\| jq`, `lg grep[^\n]*exits 5[^\n]*files`),
	Entry("finding a carried-forward job's log with lg where",
		`carried[ -]forward[^\n]*lg where`, `original_log`),
	Entry("selecting one run, which lg paths has no filter for",
		"`lg paths` has no run filter", `grep /\d+_`),
	Entry("artifacts of one name repeating across runs and attempts",
		`repeat across runs[^\n]*attempts`),
	Entry("--branch taking the branch name, not its slug",
		"`--branch`[^\n]*slug"),
	Entry("lg extract saying when it has nothing to extract",
		`nothing to extract`),
	Entry("a sample lg flakes line",
		regexp.QuoteMeta(`run 37129390741 (sha 1a51097): "flaky": 1:failure 2:success 3:success`)),
	Entry("artifacts.json as a per-attempt snapshot",
		"`artifacts.json`[^\n]*snapshot"),
	Entry("re-indented JSON",
		`re-indented`, regexp.QuoteMeta(`'"head_sha": "`)),
	Entry("jobs.json and artifacts.json as bare arrays",
		"`total_count`", "`jq '\\.\\[\\]'`"),
	Entry("the repo dir spelled as GitHub spells it",
		"GitHub's spelling"),
	Entry("names trimmed and cut to 60 bytes",
		`trimmed`, `60 bytes`),
	Entry("runs of - in names collapsing",
		"runs of `-` collapse"),
	Entry("the BOM and timestamp prefix on log lines",
		`BOM`, `timestamp prefix`),
	Entry("continue-on-error failures reporting success",
		"`continue-on-error`[^\n]*success"),
	Entry("grep exit 1 and xargs exit 123 meaning no match",
		`grep[^\n]*exit 1[^\n]*no match`, `xargs[^\n]*\b123\b`, `macOS xargs`, `stderr[^\n]*no match`),
	Entry("installing lg with go install, lg init and lg daemon install",
		`go install github\.com/rosenhouse/lg/cmd/lg@latest`, `lg init --repo`, `lg daemon install`),
	Entry("putting rg and jq on PATH, where xargs finds them",
		`ripgrep`, "`command -v rg jq`", `xargs[^\n]*alias`),
	Entry("what each --unit prints",
		"\\| `lg paths` \\| [^\n]*`log\\.txt`",
		"\\| `lg paths --unit run` \\| [^\n]*`\\.json`",
		"\\| `lg paths --unit attempt` \\| [^\n]*`attempt\\.json`",
		"\\| `lg paths --unit job` \\| [^\n]*`job\\.json`",
		"\\| `lg paths --unit log` \\| [^\n]*`log\\.txt`",
		"\\| `lg paths --unit artifact` \\| [^\n]*`artifact\\.zip`",
		"\\| `lg paths --unit extracted` \\| [^\n]*`extracted/`"),
	Entry("W3 with lg paths --sha or --pr --unit attempt and jq",
		`lg paths --sha \S+ --unit attempt[^\n]*\|[^\n]*jq`, `lg paths --pr \d+ --unit attempt[^\n]*\|[^\n]*jq`),
	Entry("--pr finding merged pull requests, and what it may miss",
		regexp.QuoteMeta("`--pr` finds the runs of open and merged pull requests from this repository, even after GitHub drops them from `pull_requests`; it may miss fork runs and runs of pull requests closed without merging, which `lg paths --event pull_request` or `lg paths --sha 1a51097` find.")),
	Entry("flakes per job and per step",
		`lg flakes`, `per job[^\n]*per[^\n]*step`),
	Entry("which runs intermittent flakes judge, and how the flakes filters differ",
		"`pull_request_target`[^\n]*cancelled",
		"attempt 1 is not on disk",
		"`lg flakes --branch[^\n]*default branch",
		"`--job` selects job names",
		"start of any attempt",
		"`--kind intermittent`[^\n]*the runs that intermittent failures compare"),
	Entry("a carried-forward failure giving a rerun flip no success",
		"carries forward a failed[^\n]*no success"),
	Entry("a date in --since or --until meaning its 00:00 UTC",
		"00:00 UTC"),
	Entry("a rerun's hits dated by the run's creation, and when the rerun ran",
		"rerun's hits[^\n]*creation date", "`run_started_at`"),
	Entry("passing -uu when rg walks the store",
		"`-uu`[^\n]*hidden"),
	Entry("age expiry, disk-cap eviction, the horizon, gc and index rebuild",
		"date dir[^\n]*`retention`", "`disk_cap`[^\n]*`extracted/`[^\n]*oldest", "horizon[^\n]*never fetches", "lg gc --dry-run", "lg index rebuild"),
)
