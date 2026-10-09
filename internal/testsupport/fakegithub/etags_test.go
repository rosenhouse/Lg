package fakegithub_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
)

var _ = Describe("Server conditional requests", Label("etags"), func() {
	var fake *fakegithub.Server

	BeforeEach(func() {
		fake = fakegithub.Start(runID, "after-attempt-1")
		Expect(fake.Load(logsDeletedRun, "logs-deleted")).To(Succeed())
		fake.SetPageCap(1)
	})

	DescribeTable("answers If-None-Match with the current ETag with a 304 that has no body or Link",
		func(path string, link types.GomegaMatcher) {
			answer := fetch(fake.URL() + path)
			Expect(answer.header.Get("ETag")).To(MatchRegexp(`^W/"[0-9a-f]{64}"$`))
			Expect(answer.header).To(link)

			again := fetch(fake.URL()+path, "If-None-Match", answer.header.Get("ETag"))
			Expect(again.status).To(Equal(http.StatusNotModified))
			Expect(again.body).To(BeEmpty())
			Expect(again.header.Get("ETag")).To(Equal(answer.header.Get("ETag")))
			Expect(again.header).NotTo(HaveKey("Link"))
		},
		Entry("the repo", "/repos/rosenhouse/lg", Not(HaveKey("Link"))),
		Entry("a run", "/repos/rosenhouse/lg/actions/runs/37129390741", Not(HaveKey("Link"))),
		Entry("a page of a run listing", "/repos/rosenhouse/lg/actions/runs?status=completed&per_page=100", HaveKey("Link")),
		Entry("a page of an artifact listing", "/repos/rosenhouse/lg/actions/runs/37129390741/artifacts?per_page=100", HaveKey("Link")),
	)

	It("sends an ETag with each blob it has, as blob storage does", func() {
		blob := fetch(fetch(fake.URL() + logPath).header.Get("Location"))
		missing := fetch(fetch(fake.URL() + "/repos/rosenhouse/lg/actions/jobs/111221290616/logs").header.Get("Location"))

		Expect(blob.status).To(Equal(http.StatusOK))
		Expect(blob.header.Get("ETag")).NotTo(BeEmpty())
		Expect(missing.status).To(Equal(http.StatusNotFound))
		Expect(missing.header).NotTo(HaveKey("Etag"))
	})

	It("answers If-None-Match with an earlier ETag in full", func() {
		path := "/repos/rosenhouse/lg/actions/runs/37129390741/artifacts?per_page=100"
		earlier := fetch(fake.URL() + path)
		Expect(fake.Advance(runID, "after-attempt-3")).To(Succeed())

		answer := fetch(fake.URL()+path, "If-None-Match", earlier.header.Get("ETag"))
		Expect(answer.status).To(Equal(http.StatusOK))
		Expect(answer.body).NotTo(Equal(earlier.body))
		Expect(answer.header.Get("ETag")).NotTo(Equal(earlier.header.Get("ETag")))
	})

	It("does not count a 304 against the rate limit, as GitHub does", func() {
		answer := fetch(fake.URL() + "/repos/rosenhouse/lg")
		Expect(answer.header.Get("X-RateLimit-Remaining")).To(Equal("4999"))

		again := fetch(fake.URL()+"/repos/rosenhouse/lg", "If-None-Match", answer.header.Get("ETag"))
		Expect(again.header.Get("X-RateLimit-Remaining")).To(Equal("4999"))
		Expect(fetch(fake.URL() + "/repos/rosenhouse/lg").header.Get("X-RateLimit-Remaining")).To(Equal("4998"))
	})

	It("records each request's If-None-Match", func() {
		fetch(fake.URL()+"/repos/rosenhouse/lg", "If-None-Match", `"abc"`)

		Expect(fake.Requests()).To(ConsistOf(HaveField("IfNoneMatch", `"abc"`)))
	})
})
