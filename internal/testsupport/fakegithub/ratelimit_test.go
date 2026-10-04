package fakegithub_test

import (
	"net/http"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("Server rate-limit headers", Label("blocked"), func() {
	var fake *fakegithub.Server

	BeforeEach(func() {
		fake = fakegithub.Start(runID, "after-attempt-1")
	})

	resetOf := func(r response) time.Time {
		GinkgoHelper()
		unix, err := strconv.ParseInt(r.header.Get("X-RateLimit-Reset"), 10, 64)
		Expect(err).NotTo(HaveOccurred())
		return time.Unix(unix, 0).UTC()
	}

	It("derives X-RateLimit-Reset from its clock, which starts at the harness's LG_TEST_NOW", func() {
		Expect(resetOf(fetch(fake.URL() + "/repos/rosenhouse/lg"))).To(Equal(harness.DefaultNow().Add(fakegithub.ResetAfter)))

		later := harness.DefaultNow().AddDate(1, 0, 0)
		fake.SetClock(clock.NewFake(later))
		Expect(resetOf(fetch(fake.URL() + "/repos/rosenhouse/lg"))).To(Equal(later.Add(fakegithub.ResetAfter)))
	})

	It("counts each API request against X-RateLimit-Limit 5000, and no blob request", func() {
		first := fetch(fake.URL() + "/repos/rosenhouse/lg")
		redirect := fetch(fake.URL() + logPath)
		blob := fetch(redirect.header.Get("Location"))

		Expect(first.header.Get("X-RateLimit-Limit")).To(Equal("5000"))
		Expect(first.header.Get("X-RateLimit-Remaining")).To(Equal("4999"))
		Expect(redirect.header.Get("X-RateLimit-Remaining")).To(Equal("4998"))
		Expect(blob.status).To(Equal(http.StatusOK))
		Expect(blob.header).NotTo(HaveKey("X-Ratelimit-Remaining"))
		Expect(fetch(fake.URL() + "/repos/rosenhouse/lg").header.Get("X-RateLimit-Remaining")).To(Equal("4997"))
	})

	It("SetRateLimit sets the limit and what remains before the next request", func() {
		fake.SetRateLimit(100, 12)

		Expect(fetch(fake.URL() + "/repos/rosenhouse/lg").header).To(SatisfyAll(
			HaveKeyWithValue("X-Ratelimit-Limit", []string{"100"}),
			HaveKeyWithValue("X-Ratelimit-Remaining", []string{"11"}),
		))
	})

	It("Fail with Headers sets them on the faulted response", func() {
		fake.Fail("api", "/repos/rosenhouse/lg", fakegithub.Fault{Status: http.StatusForbidden, Headers: map[string]string{"Retry-After": "120", "X-RateLimit-Remaining": "0"}})

		repo := fetch(fake.URL() + "/repos/rosenhouse/lg")
		Expect(repo.status).To(Equal(http.StatusForbidden))
		Expect(repo.header).To(SatisfyAll(
			HaveKeyWithValue("Retry-After", []string{"120"}),
			HaveKeyWithValue("X-Ratelimit-Remaining", []string{"0"}),
		))
	})
})
