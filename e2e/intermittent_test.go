package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// isolated matches an intermittent finding of the job, or of its step unless
// step is "", that failed alone on the runs given, in that order.
func isolated(job, step string, runs ...int64) types.GomegaMatcher {
	stepValue := BeNil()
	if step != "" {
		stepValue = Equal(step)
	}
	var failures []types.GomegaMatcher
	for _, run := range runs {
		failures = append(failures, HaveKeyWithValue("run_id", BeEquivalentTo(run)))
	}
	return SatisfyAll(
		HaveKeyWithValue("kind", "intermittent"),
		HaveKeyWithValue("job", job),
		HaveKeyWithValue("step", stepValue),
		HaveKeyWithValue("failures", HaveExactElements(failures)),
	)
}

func syncIntermittent(env *harness.Env) {
	GinkgoHelper()
	syncFrom(env, serve(scenario.Intermittent().All()...))
}

// These specs only read the store, so they share one sync.
var _ = Describe("lg flakes --kind intermittent", Ordered, ContinueOnFailure, Label("flakes"), func() {
	var env *harness.Env

	BeforeAll(func() {
		env = harness.New(lgPath)
		syncIntermittent(env)
	})

	intermittent := func(args ...string) []map[string]any {
		GinkgoHelper()
		return flakes(env, append([]string{"--kind", "intermittent"}, args...)...)
	}

	It("reports integration, which failed on one main commit and passed on the commits before and after", func() {
		Expect(lines(env, "flakes", "--kind", "intermittent")).To(ContainElement(
			`workflow "lg-fixture" on main: "integration": 1 of 6 runs failed alone: run 3 (sha 3333333) failure`,
		))
		found := intermittent()
		Expect(found).To(ContainElement(isolated("integration", "", 3)))
		for _, f := range found {
			if f["job"] == "integration" {
				id := scenario.Intermittent().Main[2].JobIDs(1, "integration")[0]
				log := filepath.Join(runDirOf(env, 3), "attempt-1", "jobs", fmt.Sprintf("%d_integration", id), "log.txt")
				Expect(json.Marshal(f)).To(MatchJSON(`{
					"kind": "intermittent",
					"workflow_id": 373958224,
					"workflow": "lg-fixture",
					"branch": "main",
					"job": "integration",
					"step": null,
					"runs": 6,
					"failures": [{"run_id": 3, "head_sha": "` + strings.Repeat("3", 40) + `", "conclusion": "failure"}],
					"logs": [` + quote(log) + `]
				}`))
			}
		}
	})

	It("does not report broken, which started failing and kept failing", func() {
		Expect(intermittent()).To(ConsistOf(isolated("integration", "", 3), isolated("suite", "unit", 4)))
	})

	It("reports step unit of job suite, which failed on one commit while the job kept failing on step e2e, and does not report the job", func() {
		found := intermittent()
		Expect(found).To(ContainElement(isolated("suite", "unit", 4)))
		Expect(found).NotTo(ContainElement(SatisfyAll(HaveKeyWithValue("job", "suite"), HaveKeyWithValue("step", BeNil()))))
	})

	It("uses attempt-1 conclusions, so the successful rerun on commit 3 does not hide its failure", func() {
		Expect(intermittent()).To(ContainElement(SatisfyAll(
			isolated("integration", "", 3),
			HaveKeyWithValue("logs", HaveExactElements(under(filepath.Join(runDirOf(env, 3), "attempt-1")))),
		)))
	})

	It("judges each failure among every run of the branch, so --sha, --since and --until select failures, not neighbours", func() {
		unitOnly := ConsistOf(SatisfyAll(isolated("suite", "unit", 4), HaveKeyWithValue("runs", BeEquivalentTo(6))))
		Expect(intermittent("--sha", "4")).To(unitOnly)
		Expect(intermittent("--since", "2026-10-01", "--until", "2026-10-01T23:59:59Z")).To(unitOnly)
	})

	It("ignores fork pull_request runs whose head branch is main, and cancelled runs", func() {
		Expect(intermittent()).To(ConsistOf(
			SatisfyAll(isolated("integration", "", 3), HaveKeyWithValue("runs", BeEquivalentTo(6))),
			SatisfyAll(isolated("suite", "unit", 4), HaveKeyWithValue("runs", BeEquivalentTo(6))),
		))
	})
})

var _ = Describe("lg flakes --kind intermittent", Label("flakes"), func() {
	It("uses default_branch from status.json and accepts --branch to override it", func() {
		env := harness.New(lgPath)
		syncIntermittent(env)
		st := env.Status()
		for _, repo := range st["repos"].(map[string]any) {
			repo.(map[string]any)["default_branch"] = "release-3"
		}
		raw, err := json.Marshal(st)
		Expect(err).NotTo(HaveOccurred())
		env.WriteStatus(string(raw))

		Expect(flakes(env, "--kind", "intermittent")).To(BeEmpty())
		Expect(flakes(env, "--kind", "intermittent", "--branch", "main")).To(ContainElement(isolated("integration", "", 3)))
	})
})

var _ = Describe("lg flakes --kind intermittent with status.json missing", Label("flakes"), func() {
	It("exits 1 naming --branch", func() {
		env := harness.New(lgPath)
		syncIntermittent(env)
		Expect(os.Remove(filepath.Join(env.State(), "status.json"))).To(Succeed())

		session := env.Lg("flakes", "--kind", "intermittent")
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(1))
		Expect(session.Err).To(gbytes.Say("--branch"))
	})
})

var _ = Describe("lg flakes --kind intermittent with run filters", Label("flakes"), func() {
	It("does not report a failure that is alone only among the runs --sha or --conclusion select", func() {
		runs := scenario.Intermittent()
		sixth := runs.Main[5]
		runs.Main[5] = scenario.SetJobConclusion(sixth, 1, sixth.JobIDs(1, "broken")[0], "success")
		runs.Main[4] = scenario.AddRerunAttempt(runs.Main[4], "broken", "suite", "flaky", "timeout")
		env := harness.New(lgPath)
		syncFrom(env, serve(runs.All()...))

		Expect(flakes(env, "--kind", "intermittent")).To(ConsistOf(isolated("integration", "", 3), isolated("suite", "unit", 4)))
		Expect(flakes(env, "--kind", "intermittent", "--sha", "1", "--sha", "2", "--sha", "3", "--sha", "5", "--sha", "6")).
			To(ConsistOf(isolated("integration", "", 3)))
		Expect(flakes(env, "--kind", "intermittent", "--conclusion", "failure")).
			To(ConsistOf(isolated("integration", "", 3), isolated("suite", "unit", 4)))
	})
})

var _ = Describe("lg flakes --kind intermittent with a pull_request run from the repository", Label("flakes"), func() {
	It("ignores the run, though its head branch is main", func() {
		pr := scenario.CloneAt(9, "after-attempt-1", time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC))
		pr = scenario.RenameJob(scenario.WithEvent(scenario.OnBranch(pr, "main"), "pull_request"), 1, "build (ubuntu-latest, 1.22)", "steady")
		pr = scenario.SetJobConclusion(pr, 1, pr.JobIDs(1, "steady")[0], "failure")
		env := harness.New(lgPath)
		syncFrom(env, serve(append(scenario.Intermittent().All(), pr)...))

		Expect(flakes(env, "--kind", "intermittent")).To(ConsistOf(isolated("integration", "", 3), isolated("suite", "unit", 4)))
	})
})
