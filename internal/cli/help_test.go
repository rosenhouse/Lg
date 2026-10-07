package cli_test

import (
	"bytes"
	"reflect"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/model"
)

// help gives the --help of args with its whitespace collapsed.
func help(args ...string) string {
	GinkgoHelper()
	var stdout bytes.Buffer
	code := cli.Main(append(args, "--help"), cli.Deps{Env: map[string]string{}, Stdout: &stdout, Stderr: &bytes.Buffer{}, Clock: clock.Real{}})
	Expect(code).To(Equal(0))
	return strings.Join(strings.Fields(stdout.String()), " ")
}

var _ = DescribeTable("--help says", Label("cli"),
	func(args []string, sentence string) {
		Expect(help(args...)).To(ContainSubstring(sentence))
	},
	Entry("how lg sync behaves with a daemon", []string{"sync"},
		"With a daemon running, it asks the daemon for a cycle and exits at once, or, with --wait, when that cycle ends."),
	Entry("how far back the first lg sync fetches", []string{"sync"},
		"The first sync fetches the runs created within backfill, "+config.Defaults().Backfill.String()+" unless config.yaml sets it."),
	Entry("how lg root spells the repo dir", []string{"root"}, "where <owner>/<repo> takes GitHub's spelling"),
	Entry("that every sync runs gc", []string{"gc"}, "Every sync does this too."),
	Entry("where lg daemon run logs", []string{"daemon", "run"}, "It logs one line per cycle to stderr."),
	Entry("what lg daemon install bakes in", []string{"daemon", "install"},
		"It bakes in lg's path, gh's dir, and whichever of LG_HOME, LG_CONFIG, LG_GH, the XDG dirs, GH_CONFIG_DIR, SSL_CERT_FILE and the proxy variables are set, so run it again after changing any of them;"),
	Entry("when to run lg index rebuild", []string{"index", "rebuild"}, "Rebuild only if lg.db is damaged."),
	Entry("that lg skill install replaces SKILL.md", []string{"skill", "install"}, "replacing any earlier copy."),
	Entry("how to pipe rg into lg where", []string{"where"},
		"lg paths -0 | xargs -0 -r rg --no-config -Hn 'foo bar' | lg where | jq -c 'del(.path)'"),
	Entry("when lg status --json fails", []string{"status"}, "It fails before the first sync."),
	Entry("what lag is", []string{"status"},
		"The lag is the time from the newest completed run's creation to the last sync's finish, so it grows with each sync that finds no newer run."),
	Entry("when lg commands warn", []string{"status"},
		"Every lg command warns on stderr while syncs are blocked. It also warns while the last successful sync, or a pending unit, is older than twice sync_interval."),
	Entry("which sync_interval the staleness warning uses", []string{"status"},
		"That sync_interval is the one the last sync used, not the one config.yaml now sets."),
	Entry("how --job matches", []string{"paths"}, "Only jobs whose whole name matches this case-sensitive glob, such as 'build*'."),
	Entry("where a date --until ends", []string{"paths"}, "A date means its 00:00 UTC, so --until 2026-10-03 stops at the start of that day."),
	Entry("how lg sync exits without a daemon", []string{"sync"},
		"Without a daemon, lg sync runs one cycle and exits with that cycle's exit code. --timeout does not limit the cycle."),
	Entry("how lg sync --wait exits", []string{"sync"}, "wait for the cycle this request starts, and exit with that cycle's exit code."),
	Entry("where lg flakes learns the default branch", []string{"flakes"}, "lg learns the default branch from GitHub at each sync; --branch replaces it."),
)

var _ = Describe("lg flakes --help", Label("flakes"), func() {
	It("names every field --json prints", func() {
		Expect(help("flakes")).To(ContainSubstring(
			"--json prints " + jsonFields(cli.FlipJSON{}) + " for a flip, and " + jsonFields(cli.IntermittentJSON{}) + " for an intermittent failure."))
	})

	It("shows a job's and a step's rerun flip lines as lg flakes prints them", func() {
		outcomes := []model.Outcome{{Attempt: 1, Conclusion: "failure"}, {Attempt: 2, Conclusion: "success"}, {Attempt: 3, Conclusion: "success"}}
		var lines bytes.Buffer
		for _, flip := range []model.Flip{
			{RunID: 37129390741, Job: "flaky", Outcomes: outcomes, FailingSteps: []string{"Fail on first attempt only"}},
			{RunID: 37129390741, Job: "flaky", Step: "Fail on first attempt only", Outcomes: outcomes},
		} {
			Expect(cli.PrintFlip(&lines, index.Flip{Flip: flip, HeadSHA: "1a51097e"})).To(Succeed())
		}
		Expect(help("flakes")).To(ContainSubstring("so one flip often gives a line for the job and a line for the step: " +
			strings.Join(strings.Fields(lines.String()), " ")))
	})
})

// jsonFields lists the json names of v's fields as prose: a, b and c.
func jsonFields(v any) string {
	t := reflect.TypeOf(v)
	var names []string
	for i := range t.NumField() {
		names = append(names, strings.Split(t.Field(i).Tag.Get("json"), ",")[0])
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
