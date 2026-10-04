package fakegithub_test

import (
	"net/url"
	"strconv"
	"strings"
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
	link := func(n, rel string) string {
		return `<http://127.0.0.1:1/repositories/1402714635/actions/runs?page=` + n + `&per_page=5&status=queued>; rel="` + rel + `"`
	}

	DescribeTable("emits prev, next, last and first in GitHub's order",
		func(n, last int, rels ...string) {
			var want []string
			for i := 0; i < len(rels); i += 2 {
				want = append(want, link(rels[i], rels[i+1]))
			}
			Expect(fakegithub.LinkHeader(page(strconv.Itoa(n)), n, last)).To(Equal(strings.Join(want, ", ")))
		},
		Entry("on the first page", 1, 3, "2", "next", "3", "last"),
		Entry("on a middle page", 2, 3, "1", "prev", "3", "next", "3", "last", "1", "first"),
		Entry("on the last page", 3, 3, "2", "prev", "1", "first"),
		Entry("on the only page", 1, 1),
	)
})
