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
		"run `lg status` first", "`lg sync --wait`[^\n]*before[^\n]*no match"),
	Entry("exit codes",
		`\| 0 \| ok`, `\| 1 \| [^\n]*pending`, `\| 2 \| usage`, `\| 3 \| blocked`, `\| 4 \| timeout`),
	Entry("finding a carried-forward job's log with lg where",
		`carried[ -]forward[^\n]*lg where`, `original_log`),
	Entry("artifacts.json as a per-attempt snapshot",
		"`artifacts.json`[^\n]*snapshot"),
	Entry("re-indented JSON",
		`re-indented`, regexp.QuoteMeta(`'"head_sha": "`)),
	Entry("jobs.json and artifacts.json as bare arrays",
		"`jobs\\.json` and `artifacts\\.json` hold one array[^\n]*without[^\n]*`total_count`[^\n]*`jq '\\.\\[\\]'`"),
	Entry("the repo dir spelled as GitHub spells it",
		"`<owner>/<repo>`[^\n]*GitHub's spelling[^\n]*`lg paths`"),
	Entry("the BOM and timestamp prefix on log lines",
		`BOM`, `timestamp prefix`),
	Entry("continue-on-error failures reporting success",
		"`continue-on-error`[^\n]*success"),
	Entry("grep exit 1, and xargs exit 123 meaning no match or an error",
		"`grep` and `rg` exit 1 when they find no match",
		`\b123 \(1 with macOS xargs\) when any grep exits non-zero: no match, or an error`,
		"Read stderr before you conclude there is no match"),
	Entry("installing lg with go install, lg init and lg daemon install",
		`go install github\.com/rosenhouse/lg/cmd/lg@latest`, `lg init --repo`, `lg daemon install`),
	Entry("what each --unit prints",
		"\\| `lg paths` \\| `log\\.txt` files and what `--unit extracted` prints",
		"\\| `lg paths --unit run` \\| every `\\.json`",
		"\\| `lg paths --unit attempt` \\| `attempt\\.json`",
		"\\| `lg paths --unit job` \\| `job\\.json`",
		"\\| `lg paths --unit log` \\| `log\\.txt`",
		"\\| `lg paths --unit artifact` \\| `artifact\\.zip`",
		"\\| `lg paths --unit extracted` \\| the files under `extracted/`"),
	Entry("W3 with lg paths --sha or --pr --unit attempt and jq",
		`lg paths --sha \S+ --unit attempt[^\n]*\|[^\n]*jq`, `lg paths --pr \d+ --unit attempt[^\n]*\|[^\n]*jq`),
	Entry("--pr missing fork pull_request runs, which --event and --sha find",
		"`--pr`[^\n]*fork[^\n]*`lg paths --event[^\n]*`lg paths --sha"),
	Entry("flakes per job and per step",
		`lg flakes`, `per job[^\n]*per[^\n]*step`),
	Entry("which runs intermittent flakes judge, and how the flakes filters differ",
		"`pull_request_target`[^\n]*cancelled",
		"attempt 1 is not on disk[^\n]*no failure next to it alone",
		"`lg flakes --branch [^`]+`[^\n]*replaces[^\n]*default branch",
		"`--job` selects job names[^\n]*`--since` and `--until` match the start of any attempt[^\n]*`--conclusion` the latest attempt",
		"For `--kind intermittent`, `--branch`, `--workflow` and `--event`[^\n]*series[^\n]*`--sha`, `--pr`, `--conclusion`, `--since` and `--until`[^\n]*failures"),
	Entry("a carried-forward failure giving a rerun flip no success",
		"carries forward a failed job or step gives its name no success"),
	Entry("a date in --since or --until meaning its 00:00 UTC",
		"date means its 00:00 UTC[^\n]*`--until 2026-10-01` stops at the start of October 1"),
	Entry("a rerun's hits dated by the run's creation, and when the rerun ran",
		"rerun's hits sit under the run's creation date[^\n]*timestamp prefix[^\n]*`run_started_at`"),
	Entry("passing -uu when rg walks the store",
		"rg walks the store[^\n]*`-uu`[^\n]*hidden[^\n]*ignore file"),
	Entry("age expiry, disk-cap eviction, the horizon, gc and index rebuild",
		"date dir[^\n]*`retention`", "`disk_cap`[^\n]*`extracted/`[^\n]*oldest", "horizon[^\n]*never fetches", "lg gc --dry-run", "lg index rebuild"),
)
