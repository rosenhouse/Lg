package failure_test

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
)

var now = time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)

func headers(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

var reset = now.Add(17 * time.Minute)

var resetUnix = strconv.FormatInt(reset.Unix(), 10)

var _ = Describe("Blocked", Label("blocked"), func() {
	It("names its kind and detail", func() {
		Expect(failure.Blocked{Kind: failure.Auth, Detail: "401 Unauthorized"}).To(MatchError("blocked (auth): 401 Unauthorized"))
	})

	It("names its retry_at in UTC when it has one", func() {
		blocked := failure.Blocked{Kind: failure.RateLimit, Detail: "429", RetryAt: now.In(time.FixedZone("PDT", -7*3600))}
		Expect(blocked).To(MatchError("blocked (rate_limit, retry_at 2026-10-03T18:00:00Z): 429"))
	})
})

var _ = DescribeTable("FromStatus", Label("blocked"),
	func(status int, header http.Header, message string, kind failure.Kind, retryAt time.Time) {
		blocked, ok := failure.FromStatus(status, header, message, now)
		Expect(ok).To(BeTrue())
		Expect(blocked).To(Equal(failure.Blocked{Kind: kind, Detail: message, RetryAt: retryAt}))
	},
	Entry("401", 401, headers(), "Bad credentials", failure.Auth, time.Time{}),
	Entry("403 with no rate-limit sign", 403, headers("X-RateLimit-Remaining", "4999", "X-RateLimit-Reset", resetUnix), "Resource not accessible by integration", failure.Auth, time.Time{}),
	Entry("429 with no Retry-After and remaining above 0", 429, headers("X-RateLimit-Remaining", "4999", "X-RateLimit-Reset", resetUnix), "Too Many Requests", failure.RateLimit, now.Add(time.Minute)),
	Entry("429 with Retry-After", 429, headers("Retry-After", "30"), "Too Many Requests", failure.RateLimit, now.Add(30*time.Second)),
	Entry("403 with Retry-After", 403, headers("Retry-After", "120", "X-RateLimit-Remaining", "0", "X-RateLimit-Reset", resetUnix), "Forbidden", failure.RateLimit, now.Add(2*time.Minute)),
	Entry("403 with X-RateLimit-Remaining 0", 403, headers("X-RateLimit-Remaining", "0", "X-RateLimit-Reset", resetUnix), "API rate limit exceeded", failure.RateLimit, reset),
	Entry("403 with X-RateLimit-Remaining 0 and no reset", 403, headers("X-RateLimit-Remaining", "0"), "API rate limit exceeded", failure.RateLimit, now.Add(time.Minute)),
	Entry("403 with 'secondary rate limit' in its message", 403, headers("X-RateLimit-Remaining", "4321", "X-RateLimit-Reset", resetUnix), "You have exceeded a secondary rate limit. Please wait a few minutes before you try again.", failure.RateLimit, now.Add(time.Minute)),
)

var _ = DescribeTable("FromStatus of a status that refuses neither credentials nor rate", Label("blocked"),
	func(status int) {
		_, ok := failure.FromStatus(status, headers("Retry-After", "30", "X-RateLimit-Remaining", "0"), "secondary rate limit", now)
		Expect(ok).To(BeFalse())
	},
	Entry("400", 400),
	Entry("404", 404),
	Entry("500", 500),
	Entry("503", 503),
)

var _ = DescribeTable("FromErrno", Label("blocked"),
	func(errno syscall.Errno) {
		err := fmt.Errorf("staging: %w", &fs.PathError{Op: "write", Path: "/lg/tmp/unit-1/attempt.json", Err: errno})
		Expect(failure.FromErrno(err)).To(Equal(failure.Blocked{Kind: failure.LocalIO, Detail: err.Error()}))
	},
	Entry("ENOSPC", syscall.ENOSPC),
	Entry("EDQUOT", syscall.EDQUOT),
	Entry("EROFS", syscall.EROFS),
	Entry("EACCES", syscall.EACCES),
	Entry("EXDEV", syscall.EXDEV),
)

var _ = Describe("FromErrno", Label("blocked"), func() {
	It("returns any other error as it is", func() {
		err := fmt.Errorf("staging: %w", syscall.EIO)
		Expect(failure.FromErrno(err)).To(BeIdenticalTo(err))
		Expect(failure.FromErrno(nil)).To(Succeed())
	})

	It("finds the errno in a joined error", func() {
		err := errors.Join(errors.New("download"), syscall.ENOSPC)
		Expect(failure.FromErrno(err)).To(HaveField("Kind", failure.LocalIO))
	})
})

var _ = DescribeTable("Reserve", Label("blocked"),
	func(header http.Header, at time.Time, blocked bool) {
		b, ok := failure.Reserve(header, at)
		Expect(ok).To(Equal(blocked))
		if blocked {
			Expect(b).To(Equal(failure.Blocked{Kind: failure.RateLimit, Detail: "X-RateLimit-Remaining 9 is below 10% of X-RateLimit-Limit 100", RetryAt: reset}))
		}
	},
	Entry("below 10% before the reset", headers("X-RateLimit-Limit", "100", "X-RateLimit-Remaining", "9", "X-RateLimit-Reset", resetUnix), now, true),
	Entry("at 10%", headers("X-RateLimit-Limit", "100", "X-RateLimit-Remaining", "10", "X-RateLimit-Reset", resetUnix), now, false),
	Entry("below 10% at the reset", headers("X-RateLimit-Limit", "100", "X-RateLimit-Remaining", "9", "X-RateLimit-Reset", resetUnix), reset, false),
	Entry("with no rate-limit headers", headers(), now, false),
	Entry("with no X-RateLimit-Limit", headers("X-RateLimit-Remaining", "9", "X-RateLimit-Reset", resetUnix), now, false),
	Entry("with no X-RateLimit-Reset", headers("X-RateLimit-Limit", "100", "X-RateLimit-Remaining", "9"), now, false),
)
