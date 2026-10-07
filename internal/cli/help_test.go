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
	Entry("how --job matches", []string{"paths"}, "Only jobs whose whole name matches this case-sensitive glob, such as 'build*'."),
	Entry("where a date --until ends", []string{"paths"}, "A date means its 00:00 UTC, so --until 2026-10-03 ends as that day starts."),
)

var _ = Describe("lg flakes --help", Label("flakes"), func() {
	It("names every field --json prints", func() {
		Expect(help("flakes")).To(ContainSubstring(
			"--json prints " + jsonFields(cli.FlipJSON{}) + " for a flip, and " + jsonFields(cli.IntermittentJSON{}) + " for an intermittent failure."))
	})

	It("shows a rerun flip line as lg flakes prints it", func() {
		var line bytes.Buffer
		Expect(cli.PrintFlip(&line, index.Flip{
			Flip: model.Flip{
				RunID: 37129390741, Job: "flaky",
				Outcomes:     []model.Outcome{{Attempt: 1, Conclusion: "failure"}, {Attempt: 2, Conclusion: "success"}, {Attempt: 3, Conclusion: "success"}},
				FailingSteps: []string{"Fail on first attempt only"},
			},
			HeadSHA: "1a51097e",
		})).To(Succeed())
		Expect(help("flakes")).To(ContainSubstring("A line reads: " + strings.TrimSpace(line.String())))
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
