package fakegithub_test

import (
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
)

var _ = Describe("the created filter", Label("transport"), func() {
	at := func(s string) time.Time {
		GinkgoHelper()
		t, err := time.Parse(time.RFC3339, s)
		Expect(err).NotTo(HaveOccurred())
		return t
	}

	DescribeTable("parses a date, >=date, a..b and RFC3339 bounds",
		func(filter string, in, out []string) {
			created, err := fakegithub.ParseCreated(filter)
			Expect(err).NotTo(HaveOccurred())
			for _, t := range in {
				Expect(created.Contains(at(t))).To(BeTrue(), t)
			}
			for _, t := range out {
				Expect(created.Contains(at(t))).To(BeFalse(), t)
			}
		},
		Entry("a date", "2026-10-03",
			[]string{"2026-10-03T00:00:00Z", "2026-10-03T23:59:59Z"},
			[]string{"2026-10-02T23:59:59Z", "2026-10-04T00:00:00Z"}),
		Entry(">=date", ">=2026-10-03",
			[]string{"2026-10-03T00:00:00Z", "2030-01-01T00:00:00Z"},
			[]string{"2026-10-02T23:59:59Z"}),
		Entry("dates a..b", "2026-10-01..2026-10-03",
			[]string{"2026-10-01T00:00:00Z", "2026-10-03T23:59:59Z"},
			[]string{"2026-09-30T23:59:59Z", "2026-10-04T00:00:00Z"}),
		Entry("RFC3339 a..b", "2026-10-03T14:00:00Z..2026-10-03T15:00:00Z",
			[]string{"2026-10-03T14:00:00Z", "2026-10-03T15:00:00Z"},
			[]string{"2026-10-03T13:59:59Z", "2026-10-03T15:00:01Z"}),
		Entry(">=RFC3339", ">=2026-10-03T14:00:00Z",
			[]string{"2026-10-03T14:00:00Z"},
			[]string{"2026-10-03T13:59:59Z"}),
	)

	DescribeTable("rejects anything else",
		func(filter string) {
			_, err := fakegithub.ParseCreated(filter)
			Expect(err).To(HaveOccurred())
		},
		Entry("a word", "yesterday"),
		Entry("an open range", "2026-10-01.."),
	)
})

var _ = Describe("the Link builder", Label("transport"), func() {
	page := func(n string) *url.URL {
		return &url.URL{Scheme: "http", Host: "127.0.0.1:1", Path: "/repositories/1402714635/actions/runs", RawQuery: "status=queued&per_page=5&page=" + n}
	}

	It("emits next and last", func() {
		Expect(fakegithub.LinkHeader(page("1"), 1, 3)).To(Equal(
			`<http://127.0.0.1:1/repositories/1402714635/actions/runs?page=2&per_page=5&status=queued>; rel="next", ` +
				`<http://127.0.0.1:1/repositories/1402714635/actions/runs?page=3&per_page=5&status=queued>; rel="last"`))
	})

	It("emits nothing on the last page", func() {
		Expect(fakegithub.LinkHeader(page("3"), 3, 3)).To(BeEmpty())
	})
})
