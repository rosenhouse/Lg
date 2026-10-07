package mirror_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

const (
	prRunID = 21
	pr      = 61
	branch  = "fix-upload"
)

// prRun is a pull_request run whose commit lists PR pr from its branch.
func prRun() scenario.Run {
	r := scenario.CloneAt(prRunID, "after-attempt-1", harness.DefaultNow().Add(-2*scenario.Day))
	r = scenario.WithEvent(scenario.OnBranch(r, branch), "pull_request")
	return scenario.WithCommitPulls(r, scenario.CommitPull{Number: pr, HeadRef: branch, HeadRepoID: scenario.RepoID})
}

func fetchOf(attemptDir string) map[string]any {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join(attemptDir, "fetch.json"))
	Expect(err).NotTo(HaveOccurred())
	var fetch map[string]any
	Expect(json.Unmarshal(raw, &fetch)).To(Succeed())
	return fetch
}

var _ = Describe("a sync whose commit lookup returns 500 once", Label("prs"), func() {
	It("leaves the attempt pending, then publishes it with commit_pr_numbers on the next cycle", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(prRun())).To(Succeed())
		env.Fake.Fail("api", "/pulls", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})

		err := env.Sync(ctx)
		Expect(err).To(MatchError(ContainSubstring("500")))
		Expect(mirror.RunScoped(err)).To(BeTrue())
		Expect(env.AttemptDirs(prRunID)).To(BeEmpty())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(fetchOf(attemptDir(env, prRunID, 1))).To(HaveKeyWithValue("commit_pr_numbers", ConsistOf(BeEquivalentTo(pr))))
	}, cycleTimeout)
})

var _ = Describe("a sync whose commit lookup returns 422", Label("prs"), func() {
	It("publishes the attempt with commit_pr_numbers []", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(prRun())).To(Succeed())
		env.Fake.Fail("api", "/pulls", fakegithub.Fault{Status: http.StatusUnprocessableEntity})

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(fetchOf(attemptDir(env, prRunID, 1))).To(SatisfyAll(
			HaveKeyWithValue("commit_pr_numbers", BeEmpty()),
			HaveKeyWithValue("sources", HaveKeyWithValue("commit_pr_numbers", HaveKeyWithValue("status", BeEquivalentTo(http.StatusUnprocessableEntity)))),
		))
	}, cycleTimeout)
})

var _ = Describe("a sync whose commit lookup returns 403 with X-Accepted-GitHub-Permissions: pull_requests=read", Label("prs"), func() {
	It("blocks as auth with a detail naming pull_requests=read", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(prRun())).To(Succeed())
		env.Fake.Fail("api", "/pulls", fakegithub.Fault{Status: http.StatusForbidden, Headers: map[string]string{"X-Accepted-GitHub-Permissions": "pull_requests=read"}})

		Expect(cycleErr(ctx, env.Mirror)).To(matchers.BeBlocked(failure.Auth, HaveField("Detail", ContainSubstring("pull_requests=read"))))
		Expect(env.AttemptDirs(prRunID)).To(BeEmpty())
	}, cycleTimeout)
})

var _ = Describe("the commit lookup", Label("prs"), func() {
	var (
		env *harness.InProcessEnv
		// rerun and pushed share a head SHA, whose commit lists PR pr from
		// branch. The cycle publishes pushed, the older, first.
		rerun  = scenario.WithCommitPulls(scenario.OnBranch(scenario.CloneAt(22, "after-attempt-2", harness.DefaultNow().Add(-2*scenario.Day)), branch), scenario.CommitPull{Number: pr, HeadRef: branch, HeadRepoID: scenario.RepoID})
		pushed = scenario.OnBranch(scenario.CloneAt(23, "after-attempt-1", harness.DefaultNow().Add(-3*scenario.Day)), "main")
	)
	const sha = "1a51097dadb5b55978ac401b93f1ca9d8d317b02"

	lookups := func() int {
		count := 0
		for _, r := range env.Fake.Requests() {
			if r.Path == "/repos/rosenhouse/lg/commits/"+sha+"/pulls" {
				count++
			}
		}
		return count
	}

	BeforeEach(func() {
		env = harness.InProcess()
		Expect(env.Fake.AddRun(rerun)).To(Succeed())
		Expect(env.Fake.AddRun(pushed)).To(Succeed())
	})

	It("looks each SHA up once per cycle, and filters the listed PRs for each run", func(ctx SpecContext) {
		Expect(env.Sync(ctx)).To(Succeed())

		Expect(lookups()).To(Equal(1))
		Expect(fetchOf(attemptDir(env, 22, 1))).To(HaveKeyWithValue("commit_pr_numbers", ConsistOf(BeEquivalentTo(pr))))
		Expect(fetchOf(attemptDir(env, 22, 2))).To(HaveKeyWithValue("commit_pr_numbers", ConsistOf(BeEquivalentTo(pr))))
		Expect(fetchOf(attemptDir(env, 23, 1))).To(HaveKeyWithValue("commit_pr_numbers", BeEmpty()))
	}, cycleTimeout)

	It("keeps a failed lookup's error for the rest of the cycle, leaving every attempt of the SHA pending", func(ctx SpecContext) {
		env.Fake.Fail("api", "/pulls", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})

		Expect(env.Sync(ctx)).To(MatchError(ContainSubstring("500")))

		Expect(lookups()).To(Equal(1))
		Expect(env.AttemptDirs(22)).To(BeEmpty())
		Expect(env.AttemptDirs(23)).To(BeEmpty())
	}, cycleTimeout)

	It("records commit_pr_numbers' source in fetch.json: the URL, status and pages", func(ctx SpecContext) {
		Expect(env.Sync(ctx)).To(Succeed())

		Expect(fetchOf(attemptDir(env, 23, 1))).To(HaveKeyWithValue("sources", HaveKeyWithValue("commit_pr_numbers", Equal(map[string]any{
			"url":    env.Fake.URL() + "/repos/rosenhouse/lg/commits/" + sha + "/pulls?per_page=100",
			"status": 200.0,
			"pages":  1.0,
		}))))
	}, cycleTimeout)
})
