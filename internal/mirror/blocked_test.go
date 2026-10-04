package mirror_test

import (
	"errors"
	"net/http"
	"os"
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
)

// blockedOf is the failure.Blocked that err wraps.
func blockedOf(err error) failure.Blocked {
	GinkgoHelper()
	var blocked failure.Blocked
	Expect(errors.As(err, &blocked)).To(BeTrue(), "want a failure.Blocked, got %v", err)
	return blocked
}

func blockedAs(kind failure.Kind, retryAt time.Time) types.GomegaMatcher {
	return SatisfyAll(HaveField("Kind", kind), HaveField("RetryAt", BeTemporally("==", retryAt)))
}

// failAttempts answers every request for a run's attempt 1 with f.
func failAttempts(f fakegithub.Fault) func(*harness.InProcessEnv) {
	return func(env *harness.InProcessEnv) { env.Fake.Fail("api", "/attempts/1", f) }
}

func requestsEndingIn(env *harness.InProcessEnv, suffix string) []fakegithub.Request {
	var matched []fakegithub.Request
	for _, r := range env.Fake.Requests() {
		if strings.HasSuffix(r.Path, suffix) {
			matched = append(matched, r)
		}
	}
	return matched
}

var _ = DescribeTable("mirror.Cycle returns Blocked and makes no further request", Label("blocked"),
	func(ctx SpecContext, inject func(*harness.InProcessEnv), want types.GomegaMatcher) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		inject(env)

		err := env.Mirror.Cycle(ctx)
		Expect(err).To(BeAssignableToTypeOf(failure.Blocked{}))
		Expect(err).To(want)
		Expect(requestsEndingIn(env, "/attempts/1")).To(Or(BeEmpty(), HaveLen(1)))
		Expect(env.AttemptDirs(runID)).To(BeEmpty())
		Expect(env.AttemptDirs(deletedRun)).To(BeEmpty())
		Expect(os.ReadDir(env.Tmp())).To(BeEmpty())
	},
	Entry("401: auth",
		failAttempts(fakegithub.Fault{Status: http.StatusUnauthorized}),
		blockedAs(failure.Auth, time.Time{}), cycleTimeout),
	Entry("API 403 with no rate-limit sign: auth",
		failAttempts(fakegithub.Fault{Status: http.StatusForbidden}),
		blockedAs(failure.Auth, time.Time{}), cycleTimeout),
	Entry("403 with X-RateLimit-Remaining 0: rate_limit, retry_at from X-RateLimit-Reset",
		failAttempts(fakegithub.Fault{Status: http.StatusForbidden, Headers: map[string]string{
			"X-RateLimit-Remaining": "0",
			"X-RateLimit-Reset":     strconv.FormatInt(harness.DefaultNow().Add(17*time.Minute).Unix(), 10),
		}}),
		blockedAs(failure.RateLimit, harness.DefaultNow().Add(17*time.Minute)), cycleTimeout),
	Entry("403 with Retry-After: rate_limit, retry_at from Retry-After",
		failAttempts(fakegithub.Fault{Status: http.StatusForbidden, Headers: map[string]string{"Retry-After": "120"}}),
		blockedAs(failure.RateLimit, harness.DefaultNow().Add(2*time.Minute)), cycleTimeout),
	Entry("429 with Retry-After: rate_limit",
		failAttempts(fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "30"}}),
		blockedAs(failure.RateLimit, harness.DefaultNow().Add(30*time.Second)), cycleTimeout),
	Entry("secondary-limit 403 with X-RateLimit-Remaining above 0, no Retry-After and 'secondary rate limit' in its message: rate_limit 60s later",
		failAttempts(fakegithub.Fault{Status: http.StatusForbidden, Headers: map[string]string{"X-RateLimit-Remaining": "4321"}, Body: fakegithub.SecondaryLimitBody}),
		blockedAs(failure.RateLimit, harness.DefaultNow().Add(time.Minute)), cycleTimeout),
	Entry("429 on a log, once its unit is staged: rate_limit",
		func(env *harness.InProcessEnv) {
			env.Fake.Fail("api", "/logs", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "30"}})
		},
		blockedAs(failure.RateLimit, harness.DefaultNow().Add(30*time.Second)), cycleTimeout),
	Entry("refused connection: unreachable, naming the host",
		func(env *harness.InProcessEnv) { env.Fake.Close() },
		SatisfyAll(blockedAs(failure.Unreachable, time.Time{}), HaveField("Detail", MatchRegexp(`127\.0\.0\.1:\d+`))), cycleTimeout),
	Entry("ENOSPC writing a member: local_io",
		func(env *harness.InProcessEnv) { env.FS.FailOn("write", syscall.ENOSPC) },
		blockedAs(failure.LocalIO, time.Time{}), cycleTimeout),
	Entry("EXDEV publishing: local_io",
		func(env *harness.InProcessEnv) { env.FS.FailOn("rename", syscall.EXDEV) },
		blockedAs(failure.LocalIO, time.Time{}), cycleTimeout),
)

var _ = Describe("mirror.Cycle", Label("blocked"), func() {
	It("stops as rate_limit with retry_at at the reset once X-RateLimit-Remaining falls below 10% of X-RateLimit-Limit", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		// The third API response says 9 of 100 remain.
		env.Fake.SetRateLimit(100, 12)

		Expect(blockedOf(env.Mirror.Cycle(ctx))).To(blockedAs(failure.RateLimit, harness.DefaultNow().Add(fakegithub.ResetAfter)))
		Expect(env.Fake.Requests()).To(HaveLen(3))
		Expect(env.AttemptDirs(runID)).To(BeEmpty())
	}, cycleTimeout)
})
