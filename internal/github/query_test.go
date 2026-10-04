package github_test

import (
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
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
})
