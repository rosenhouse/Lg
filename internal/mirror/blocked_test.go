package mirror_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
)

func blockedAs(kind failure.Kind, retryAt time.Time) types.GomegaMatcher {
	return BeBlocked(kind, HaveField("RetryAt", BeTemporally("==", retryAt)))
}

// failAttempts answers every request for a run's attempt 1 with f.
func failAttempts(f fakegithub.Fault) func(*harness.InProcessEnv) {
	return func(env *harness.InProcessEnv) { env.Fake.Fail("api", "/attempts/1", f) }
}

func endingIn(requests []fakegithub.Request, suffix string) []fakegithub.Request {
	var matched []fakegithub.Request
	for _, r := range requests {
		if strings.HasSuffix(r.Path, suffix) {
			matched = append(matched, r)
		}
	}
	return matched
}

// endWithOnly matches requests whose last is the only one ending in suffix, answered with status.
func endWithOnly(suffix string, status int) types.GomegaMatcher {
	return SatisfyAll(
		WithTransform(func(requests []fakegithub.Request) []fakegithub.Request { return endingIn(requests, suffix) }, HaveLen(1)),
		WithTransform(func(requests []fakegithub.Request) fakegithub.Request { return requests[len(requests)-1] },
			SatisfyAll(HaveField("Path", HaveSuffix(suffix)), HaveField("Status", status))),
	)
}

// listedOneRunsArtifacts matches requests that list the artifacts of exactly one run.
func listedOneRunsArtifacts() types.GomegaMatcher {
	return WithTransform(func(requests []fakegithub.Request) []fakegithub.Request { return endingIn(requests, "/artifacts") }, HaveLen(1))
}

var _ = DescribeTable("mirror.Cycle returns Blocked and makes no further request", Label("blocked"),
	func(ctx SpecContext, inject func(*harness.InProcessEnv), want, requests types.GomegaMatcher) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		inject(env)

		err := env.Mirror.Cycle(ctx)
		Expect(err).To(BeAssignableToTypeOf(failure.Blocked{}))
		Expect(err).To(want)
		Expect(env.Fake.Requests()).To(requests)
		Expect(env.AttemptDirs(runID)).To(BeEmpty())
		Expect(env.AttemptDirs(deletedRun)).To(BeEmpty())
		Expect(os.ReadDir(env.Tmp())).To(BeEmpty())
	},
	Entry("401: auth, advising `gh auth login`",
		failAttempts(fakegithub.Fault{Status: http.StatusUnauthorized}),
		BeBlocked(failure.Auth, HaveField("Detail", HaveSuffix("; run `gh auth login --hostname github.com`"))),
		endWithOnly("/attempts/1", http.StatusUnauthorized), cycleTimeout),
	Entry("API 403 with no rate-limit sign: auth, advising nothing",
		failAttempts(fakegithub.Fault{Status: http.StatusForbidden}),
		BeBlocked(failure.Auth, HaveField("Detail", Not(ContainSubstring("gh auth login")))),
		endWithOnly("/attempts/1", http.StatusForbidden), cycleTimeout),
	Entry("403 with X-RateLimit-Remaining 0: rate_limit, retry_at from X-RateLimit-Reset",
		failAttempts(fakegithub.Fault{Status: http.StatusForbidden, Headers: map[string]string{
			"X-RateLimit-Remaining": "0",
			"X-RateLimit-Reset":     strconv.FormatInt(harness.DefaultNow().Add(17*time.Minute).Unix(), 10),
		}}),
		blockedAs(failure.RateLimit, harness.DefaultNow().Add(17*time.Minute)), endWithOnly("/attempts/1", http.StatusForbidden), cycleTimeout),
	Entry("403 with Retry-After: rate_limit, retry_at from Retry-After",
		failAttempts(fakegithub.Fault{Status: http.StatusForbidden, Headers: map[string]string{"Retry-After": "120"}}),
		blockedAs(failure.RateLimit, harness.DefaultNow().Add(2*time.Minute)), endWithOnly("/attempts/1", http.StatusForbidden), cycleTimeout),
	Entry("429 with Retry-After: rate_limit",
		failAttempts(fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "30"}}),
		blockedAs(failure.RateLimit, harness.DefaultNow().Add(30*time.Second)), endWithOnly("/attempts/1", http.StatusTooManyRequests), cycleTimeout),
	Entry("secondary-limit 403 with X-RateLimit-Remaining above 0, no Retry-After and 'secondary rate limit' in its message: rate_limit 60s later",
		failAttempts(fakegithub.Fault{Status: http.StatusForbidden, Headers: map[string]string{"X-RateLimit-Remaining": "4321"}, Body: fakegithub.SecondaryLimitBody}),
		blockedAs(failure.RateLimit, harness.DefaultNow().Add(time.Minute)), endWithOnly("/attempts/1", http.StatusForbidden), cycleTimeout),
	Entry("429 on a log, once its unit is staged: rate_limit",
		func(env *harness.InProcessEnv) {
			env.Fake.Fail("api", "/logs", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "30"}})
		},
		blockedAs(failure.RateLimit, harness.DefaultNow().Add(30*time.Second)), endWithOnly("/logs", http.StatusTooManyRequests), cycleTimeout),
	Entry("refused connection: unreachable, naming the host",
		func(env *harness.InProcessEnv) { env.Fake.Close() },
		SatisfyAll(blockedAs(failure.Unreachable, time.Time{}), HaveField("Detail", MatchRegexp(`127\.0\.0\.1:\d+`))), BeEmpty(), cycleTimeout),
	Entry("ENOSPC writing a member: local_io",
		func(env *harness.InProcessEnv) { env.FS.FailOnUnder("write", env.Tmp(), syscall.ENOSPC) },
		blockedAs(failure.LocalIO, time.Time{}), SatisfyAll(listedOneRunsArtifacts(), endWithOnly("/runs/37129738159", http.StatusOK)), cycleTimeout),
	Entry("EXDEV publishing: local_io",
		func(env *harness.InProcessEnv) { env.FS.FailOnUnder("rename", env.Data(), syscall.EXDEV) },
		blockedAs(failure.LocalIO, time.Time{}), SatisfyAll(listedOneRunsArtifacts(), endWithOnly(".zip", http.StatusOK)), cycleTimeout),
	Entry("EXDEV replacing state/pending-artifacts.json, before any zip: local_io",
		func(env *harness.InProcessEnv) {
			Expect(os.WriteFile(filepath.Join(env.State(), "pending-artifacts.json"), []byte(`{"github.com":{}}`), 0o644)).To(Succeed())
			env.FS.FailOnUnder("rename", env.State(), syscall.EXDEV)
		},
		blockedAs(failure.LocalIO, time.Time{}), SatisfyAll(listedOneRunsArtifacts(), endWithOnly("/runs/37129738159", http.StatusOK)), cycleTimeout),
	Entry("EXDEV rebuilding a missing state/pending-artifacts.json, before any run listing: local_io",
		func(env *harness.InProcessEnv) { env.FS.FailOnUnder("rename", env.State(), syscall.EXDEV) },
		blockedAs(failure.LocalIO, time.Time{}), endWithOnly("/repos/rosenhouse/lg", http.StatusOK), cycleTimeout),
)

var _ = Describe("mirror.Cycle", Label("blocked"), func() {
	It("stops as rate_limit with retry_at at the reset once X-RateLimit-Remaining falls below 10% of X-RateLimit-Limit", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		// The third API response says 9 of 100 remain.
		env.Fake.SetRateLimit(100, 12)

		Expect(env.Mirror.Cycle(ctx)).To(blockedAs(failure.RateLimit, harness.DefaultNow().Add(fakegithub.ResetAfter)))
		Expect(env.Fake.Requests()).To(HaveLen(3))
		Expect(env.AttemptDirs(runID)).To(BeEmpty())
	}, cycleTimeout)
})
