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
