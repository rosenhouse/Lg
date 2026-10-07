package e2e_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

// attemptFiles are the files that lg paths --unit attempt lists for attempt 1.
func attemptFiles(runDir string) []string {
	var files []string
	for _, name := range []string{"attempt.json", "jobs.json", "artifacts.json", "fetch.json"} {
		files = append(files, filepath.Join(runDir, "attempt-1", name))
	}
	return files
}

// These specs only read the store, so they share one sync.
var _ = Describe("lg paths --pr over runs whose pull_requests GitHub emptied", Ordered, ContinueOnFailure, Label("prs"), func() {
	const (
		pr     = 61
		forkPR = 62
		branch = "fix-upload"
	)
	var (
		env *harness.Env

		hoursAgo = func(h int) time.Time { return harness.DefaultNow().Add(-time.Duration(h) * time.Hour) }
		push     = func(id int64, sha rune, h int) scenario.Run {
			return scenario.WithSHA(scenario.OnBranch(scenario.CloneAt(id, "after-attempt-1", hoursAgo(h)), "main"), strings.Repeat(string(sha), 40))
		}
		fromBranch = scenario.CommitPull{Number: pr, HeadRef: branch, HeadRepoID: scenario.RepoID}

		prRun = scenario.WithCommitPulls(scenario.WithoutArtifacts(scenario.WithEvent(scenario.OnBranch(push(11, 'a', 30), branch), "pull_request")), fromBranch)
		// sameSHA is a push on main of prRun's head SHA, synced before prRun.
		sameSHA    = push(12, 'a', 31)
		squash     = scenario.WithCommitPulls(push(13, 'b', 28), fromBranch)
		forkSquash = scenario.WithCommitPulls(push(14, 'c', 27), scenario.CommitPull{Number: forkPR, HeadRef: "main"})
	)

	BeforeAll(func() {
		env = harness.New(lgPath)
		fake := fakegithub.New()
		DeferCleanup(fake.Close)
		for _, r := range []scenario.Run{prRun, sameSHA, squash, forkSquash} {
			Expect(fake.AddRun(r)).To(Succeed())
		}
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
	})

	paths := func(args ...string) []string {
		GinkgoHelper()
		return lines(env, append([]string{"paths"}, args...)...)
	}

	Describe("a pull_request run whose pull_requests GitHub emptied after its PR merged", func() {
		It("is listed by lg paths --pr N --unit attempt, found through the commit's pull requests", func() {
			Expect(paths("--pr", strconv.Itoa(pr), "--unit", "attempt")).To(ContainElements(attemptFiles(runDirOf(env, prRun.ID))))
		})

		It("gives lg where prs [N]", func() {
			log := glob(runDirOf(env, prRun.ID), "attempt-1", "jobs", "*_pass", "log.txt")[0]
			Expect(where(env, log)).To(ConsistOf(HaveKeyWithValue("prs", ConsistOf(BeEquivalentTo(pr)))))
		})
	})

	Describe("the push run on main of the squash commit that merged PR N", func() {
		It("is not listed by lg paths --pr N", func() {
			Expect(paths("--pr", strconv.Itoa(pr))).NotTo(ContainElement(under(runDirOf(env, squash.ID))))
		})
	})

	Describe("the push run on main of the squash commit that merged PR N from a deleted fork's main", func() {
		It("is not listed by lg paths --pr N", func() {
			Expect(paths("--pr", strconv.Itoa(forkPR))).NotTo(ContainElement(under(runDirOf(env, forkSquash.ID))))
		})
	})

	Describe("a pull_request run on branch b and a push run on main with the same head SHA", func() {
		It("lists only the pull_request run for lg paths --pr N --unit attempt", func() {
			Expect(paths("--pr", strconv.Itoa(pr), "--unit", "attempt")).To(ConsistOf(attemptFiles(runDirOf(env, prRun.ID))))
		})
	})
})
