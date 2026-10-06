package e2e_test

import (
	"encoding/json"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// syncThroughAttempt3 syncs the fixture run at each stage up to
// after-attempt-3, with the others served throughout.
func syncThroughAttempt3(env *harness.Env, others ...scenario.Run) {
	GinkgoHelper()
	fake := fakegithub.Start(fixtureRun, "after-attempt-1")
	for _, r := range others {
		Expect(fake.AddRun(r)).To(Succeed())
	}
	env.WriteConfig(fake.URL())
	for _, stage := range []string{"after-attempt-1", "after-attempt-2", "after-attempt-3"} {
		Expect(fake.Advance(fixtureRun, stage)).To(Succeed())
		Expect(env.Sync()).To(gexec.Exit(0))
	}
}

// syncRuns syncs the runs alone.
func syncRuns(env *harness.Env, runs ...scenario.Run) {
	GinkgoHelper()
	fake := fakegithub.New()
	DeferCleanup(fake.Close)
	for _, r := range runs {
		Expect(fake.AddRun(r)).To(Succeed())
	}
	env.WriteConfig(fake.URL())
	Expect(env.Sync()).To(gexec.Exit(0))
}

// lgOK runs lg with args to exit 0.
func lgOK(env *harness.Env, args ...string) *gexec.Session {
	GinkgoHelper()
	session := env.Lg(args...)
	Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
	return session
}

// flakes runs lg flakes --json with args to exit 0 and decodes what it printed.
func flakes(env *harness.Env, args ...string) []map[string]any {
	GinkgoHelper()
	return decoded(lgOK(env, append([]string{"flakes", "--json"}, args...)...))
}

// finding matches a finding of the job, or of its step unless step is "".
func finding(job, step string, attempts []int, conclusions ...string) types.GomegaMatcher {
	var stepValue any
	if step != "" {
		stepValue = step
	}
	var attemptValues []any
	for _, a := range attempts {
		attemptValues = append(attemptValues, BeEquivalentTo(a))
	}
	return SatisfyAll(
		HaveKeyWithValue("job", job),
		HaveKeyWithValue("step", stepValue),
		HaveKeyWithValue("attempts", HaveExactElements(attemptValues...)),
		HaveKeyWithValue("conclusions", HaveExactElements(toAny(conclusions)...)),
	)
}

func toAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// findingsOf keeps the findings of the job, leaving out its steps' unless steps.
func findingsOf(all []map[string]any, job string, steps bool) []map[string]any {
	var kept []map[string]any
	for _, f := range all {
		if f["job"] == job && (steps || f["step"] == nil) {
			kept = append(kept, f)
		}
	}
	return kept
}

const (
	stepA = "Build nested archives"
	stepB = "Run actions/upload-artifact@v4"
)

// stepFlip is the fixture run's attempt 1 as run id, where job "pass" fails
// on step A and skips step B, and re-run in attempt 2, where A passes and B fails.
func stepFlip(id int64) scenario.Run {
	r := scenario.Clone(scenario.Recorded(fixtureRun, "after-attempt-1"), id)
	pass := r.JobIDs(1, "pass")[0]
	r = scenario.SetStepConclusion(r, 1, pass, stepA, "failure")
	r = scenario.SetStepConclusion(r, 1, pass, stepB, "skipped")
	r = scenario.SetJobConclusion(r, 1, pass, "failure")
	r = scenario.AddRerunAttempt(r, "pass")
	rerun := r.JobIDs(2, "pass")[0]
	r = scenario.SetStepConclusion(r, 2, rerun, stepB, "failure")
	return scenario.SetJobConclusion(r, 2, rerun, "failure")
}

// carriedFlip is the fixture run's attempt 1 as run id, where jobs "flaky"
// and "pass" fail. Attempt 2 re-runs "flaky" and carries "pass" forward;
// attempt 3 re-runs "pass".
func carriedFlip(id int64) scenario.Run {
	r := scenario.Clone(scenario.Recorded(fixtureRun, "after-attempt-1"), id)
	r = scenario.SetJobConclusion(r, 1, r.JobIDs(1, "pass")[0], "failure")
	return scenario.AddRerunAttempt(scenario.AddRerunAttempt(r, "flaky"), "pass")
}

// sameNameFlip is the fixture run's attempt 1 as run id, where the first of
// its two jobs named "same name" fails, and attempt 2 re-runs both.
func sameNameFlip(id int64) scenario.Run {
	r := scenario.Clone(scenario.Recorded(fixtureRun, "after-attempt-1"), id)
	r = scenario.SetJobConclusion(r, 1, r.JobIDs(1, "same name")[0], "failure")
	return scenario.AddRerunAttempt(r, "same name")
}

// These specs only read the store, so they share one sync.
var _ = Describe("lg flakes after syncing run 37129390741 through after-attempt-3", Ordered, ContinueOnFailure, Label("flakes"), func() {
	var env *harness.Env

	BeforeAll(func() {
		env = harness.New(lgPath)
		syncThroughAttempt3(env)
	})

	It("reports flaky (1:failure 2:success 3:success, failing step named) and timeout (1:cancelled 2:success 3:success), and the steps flaky / 'Fail on first attempt only' and timeout / 'Time out on first attempt only' with the same outcomes", func() {
		Expect(outputLines(lgOK(env, "flakes"))).To(ContainElements(
			`run 37129390741 (sha 1a51097): "flaky": 1:failure 2:success 3:success; failing steps: "Fail on first attempt only"`,
			`run 37129390741 (sha 1a51097): "flaky" / "Fail on first attempt only": 1:failure 2:success 3:success`,
			`run 37129390741 (sha 1a51097): "timeout": 1:cancelled 2:success 3:success; failing steps: "Time out on first attempt only"`,
			`run 37129390741 (sha 1a51097): "timeout" / "Time out on first attempt only": 1:cancelled 2:success 3:success`,
		))
	})

	It("does not report after-flaky (skipped, then success), the step flaky / 'Upload artifact on reruns only' (skipped, then success), or any job or step that never flipped", func() {
		Expect(flakes(env)).To(ConsistOf(
			finding("flaky", "", []int{1, 2, 3}, "failure", "success", "success"),
			finding("flaky", "Fail on first attempt only", []int{1, 2, 3}, "failure", "success", "success"),
			finding("timeout", "", []int{1, 2, 3}, "cancelled", "success", "success"),
			finding("timeout", "Time out on first attempt only", []int{1, 2, 3}, "cancelled", "success", "success"),
		))
	})
})

var _ = Describe("a run where step A fails and step B is skipped in attempt 1, and A passes and B fails in attempt 2", Label("flakes"), func() {
	It("reports step A as 1:failure 2:success although its job went failure→failure, and does not report the job", func() {
		env := harness.New(lgPath)
		syncRuns(env, stepFlip(1))

		Expect(flakes(env)).To(HaveExactElements(finding("pass", stepA, []int{1, 2}, "failure", "success")))
	})
})

var _ = Describe("a run where re-running one job carries another job's failure forward", Label("flakes"), func() {
	It("reports that job as 1:failure 3:success, listing only log paths that exist", func() {
		env := harness.New(lgPath)
		syncRuns(env, carriedFlip(1))

		pass := findingsOf(flakes(env), "pass", false)
		Expect(pass).To(HaveExactElements(finding("pass", "", []int{1, 3}, "failure", "success")))
		run := runDirOf(env, 1)
		Expect(pass[0]["logs"]).To(HaveExactElements(
			SatisfyAll(HavePrefix(filepath.Join(run, "attempt-1", "jobs")+"/"), HaveSuffix("_pass/log.txt"), BeARegularFile()),
			SatisfyAll(HavePrefix(filepath.Join(run, "attempt-3", "jobs")+"/"), HaveSuffix("_pass/log.txt"), BeARegularFile()),
		))
	})
})

var _ = Describe("a run where one of two jobs named 'same name' fails in attempt 1 and both pass in attempt 2", Label("flakes"), func() {
	It("reports 'same name' as 1:failure 2:success", func() {
		env := harness.New(lgPath)
		syncRuns(env, sameNameFlip(1))

		Expect(findingsOf(flakes(env), "same name", false)).To(HaveExactElements(finding("same name", "", []int{1, 2}, "failure", "success")))
	})
})

var _ = Describe("lg flakes", Ordered, ContinueOnFailure, Label("flakes"), func() {
	const other = 2
	var env *harness.Env

	BeforeAll(func() {
		env = harness.New(lgPath)
		syncThroughAttempt3(env, scenario.OnBranch(scenario.WithSHA(sameNameFlip(other), strings.Repeat("2", 40)), "main"))
	})

	runIDs := func(args ...string) []any {
		var ids []any
		for _, f := range flakes(env, args...) {
			ids = append(ids, f["run_id"])
		}
		return ids
	}

	It("applies --sha 1a51097 and the lg paths filters, and with --json prints one object per finding with run_id, head_sha, job, step (null for a job finding), attempts, conclusions, failing steps and log paths", func() {
		Expect(runIDs()).To(ContainElements(BeEquivalentTo(fixtureRun), BeEquivalentTo(other)))
		Expect(runIDs("--sha", "1a51097")).To(HaveEach(BeEquivalentTo(fixtureRun)))
		Expect(runIDs("--branch", "main")).To(HaveExactElements(BeEquivalentTo(other)))
		Expect(flakes(env, "--job", "same*")).To(HaveExactElements(HaveKeyWithValue("run_id", BeEquivalentTo(other))))
		Expect(runIDs("--since", "2026-10-03T14:25:00Z")).To(BeEmpty())

		jobs := filepath.Join(env.Data(), fixtureRunDir)
		flaky := findingsOf(flakes(env, "--sha", "1a51097"), "flaky", false)
		Expect(flaky).To(HaveLen(1))
		Expect(json.Marshal(flaky[0])).To(MatchJSON(`{
			"kind": "rerun",
			"run_id": 37129390741,
			"head_sha": "1a51097dadb5b55978ac401b93f1ca9d8d317b02",
			"job": "flaky",
			"step": null,
			"attempts": [1, 2, 3],
			"conclusions": ["failure", "success", "success"],
			"failing_steps": ["Fail on first attempt only"],
			"logs": [
				` + quote(filepath.Join(jobs, "attempt-1", "jobs", "111221289888_flaky", "log.txt")) + `,
				` + quote(filepath.Join(jobs, "attempt-2", "jobs", "111221661475_flaky", "log.txt")) + `,
				` + quote(filepath.Join(jobs, "attempt-3", "jobs", "111221892393_flaky", "log.txt")) + `
			]
		}`))
	})
})
