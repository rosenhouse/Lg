package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("RunQuery", Label("discovery"), func() {
	It("encodes created=<from>..<to> in RFC3339 UTC, status, per_page and page", func() {
		q := github.RunQuery{
			From:    time.Date(2026, 9, 26, 20, 0, 0, 0, time.FixedZone("CEST", 2*3600)),
			To:      time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC),
			Status:  "queued",
			PerPage: 100,
			Page:    2,
		}

		Expect(q.Values()).To(Equal(url.Values{
			"created":  {"2026-09-26T18:00:00Z..2026-10-03T18:00:00Z"},
			"status":   {"queued"},
			"per_page": {"100"},
			"page":     {"2"},
		}))
	})

	It("omits what is unset", func() {
		Expect(github.RunQuery{Status: "in_progress"}.Values()).To(Equal(url.Values{"status": {"in_progress"}}))
	})

	DescribeTable("is narrowable with no created range, or with one whose bounds are different seconds",
		func(q github.RunQuery, narrowable bool) {
			Expect(q.Narrowable()).To(Equal(narrowable))
		},
		Entry("a status alone", github.RunQuery{Status: "queued"}, true),
		Entry("a day", github.RunQuery{From: at, To: at.Add(scenario.Day)}, true),
		Entry("two seconds", github.RunQuery{From: at, To: at.Add(2 * time.Second)}, true),
		Entry("one second", github.RunQuery{From: at, To: at.Add(time.Second)}, true),
		Entry("an instant", github.RunQuery{From: at, To: at}, false),
	)
})

var at = time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)

var _ = Describe("ListRuns", Label("discovery"), func() {
	newClient := func(fake *fakegithub.Server) *github.HTTP {
		return github.NewHTTP(http.DefaultTransport, mustParse(fake.URL()), "rosenhouse/lg", "lg-test-token", clock.Real{})
	}

	It("stops after the first page when total_count reaches ListingCap, since GitHub serves no more than that of a filtered listing", func() {
		fake := fakegithub.New()
		DeferCleanup(fake.Close)
		for i := range int64(github.ListingCap) {
			fake.AddListed(scenario.ListedRun(i+1, at.Add(time.Duration(i)*time.Minute)))
		}

		listing, err := newClient(fake).ListRuns(context.Background(), github.RunQuery{From: at, To: at.Add(scenario.Day)})
		Expect(err).NotTo(HaveOccurred())
		Expect(listing).To(HaveField("Total", github.ListingCap))
		Expect(listing).To(HaveField("Capped", true))
		Expect(listing.Runs).To(HaveLen(100))
		Expect(fake.Requests()).To(HaveLen(1))
	})

	It("follows every page of a capped listing that cannot be narrowed", func() {
		fake := fakegithub.New()
		DeferCleanup(fake.Close)
		for i := range int64(github.ListingCap + 1) {
			fake.AddListed(scenario.ListedRun(i+1, at))
		}

		listing, err := newClient(fake).ListRuns(context.Background(), github.RunQuery{From: at, To: at})
		Expect(err).NotTo(HaveOccurred())
		Expect(listing).To(HaveField("Total", github.ListingCap+1))
		Expect(listing).To(HaveField("Capped", true))
		Expect(listing.Runs).To(HaveLen(github.ListingCap))
		Expect(fake.Requests()).To(HaveLen(10))
	})

	It("follows every page of a listing under ListingCap", func() {
		fake := fakegithub.New()
		DeferCleanup(fake.Close)
		for i := range int64(github.ListingCap - 1) {
			fake.AddListed(scenario.ListedRun(i+1, at.Add(time.Duration(i)*time.Minute)))
		}

		listing, err := newClient(fake).ListRuns(context.Background(), github.RunQuery{From: at, To: at.Add(scenario.Day)})
		Expect(err).NotTo(HaveOccurred())
		Expect(listing).To(HaveField("Total", github.ListingCap-1))
		Expect(listing).To(HaveField("Capped", false))
		Expect(listing.Runs).To(HaveLen(github.ListingCap - 1))
		Expect(fake.Requests()).To(HaveLen(10))
	})

	It("reports a listing whose last page has no Link next as complete, whatever its total_count", func() {
		fake := fakegithub.New()
		DeferCleanup(fake.Close)
		body := fmt.Sprintf(`{"total_count":%d,"workflow_runs":[%s]}`, github.ListingCap, scenario.ListedRun(1, at))
		fake.Fail("api", "/actions/runs", fakegithub.Fault{Status: http.StatusOK, Body: body})

		listing, err := newClient(fake).ListRuns(context.Background(), github.RunQuery{From: at, To: at.Add(scenario.Day)})
		Expect(err).NotTo(HaveOccurred())
		Expect(listing).To(HaveField("Capped", false))
		Expect(listing.Runs).To(HaveLen(1))
	})
})
