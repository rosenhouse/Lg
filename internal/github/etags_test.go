package github_test

import (
	"bytes"
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
)

var _ = Describe("HTTP with a Cache", Label("etags"), func() {
	const (
		runID      = 37129390741
		otherRunID = 37129738159
		repoURL    = "/repos/rosenhouse/lg"
	)

	var (
		fake   *fakegithub.Server
		cache  *github.Cache
		client *github.HTTP
	)

	BeforeEach(func() {
		fake = fakegithub.Start(runID, "after-attempt-1")
		Expect(fake.Load(otherRunID, "logs-deleted")).To(Succeed())
		fake.SetPageCap(1)
		cache = github.NewCache(nil)
		client = github.NewHTTP(http.DefaultTransport, mustParse(fake.URL()), "rosenhouse/lg", "lg-test-token", clock.Real{}).WithCache(cache)
	})

	since := func(n int) []fakegithub.Request { return fake.Requests()[n:] }
	day := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)

	DescribeTable("repeats a GET with the ETag of its earlier answer, and reads that answer again on a 304",
		func(call func(*github.HTTP) (any, error), pages int) {
			first, err := call(client)
			Expect(err).NotTo(HaveOccurred())
			Expect(since(0)).To(HaveLen(pages))
			Expect(since(0)).To(HaveEach(HaveField("IfNoneMatch", "")))

			again, err := call(client)
			Expect(err).NotTo(HaveOccurred())
			Expect(again).To(Equal(first))
			Expect(since(pages)).To(HaveLen(pages))
			Expect(since(pages)).To(HaveEach(SatisfyAll(
				HaveField("IfNoneMatch", Not(BeEmpty())),
				HaveField("Status", http.StatusNotModified),
			)))
		},
		Entry("GetRepo", func(c *github.HTTP) (any, error) { return c.GetRepo(context.Background()) }, 1),
		Entry("GetRun", func(c *github.HTTP) (any, error) { return c.GetRun(context.Background(), runID) }, 1),
		Entry("ListRuns by status, on every page", func(c *github.HTTP) (any, error) {
			return c.ListRuns(context.Background(), github.RunQuery{Status: "completed"})
		}, 2),
		Entry("ListArtifacts, on every page", func(c *github.HTTP) (any, error) {
			artifacts, source, err := c.ListArtifacts(context.Background(), runID)
			return []any{artifacts, source}, err
		}, 4),
	)

	DescribeTable("sends no If-None-Match on a GET whose URL is not repeated",
		func(call func(*github.HTTP) error) {
			Expect(call(client)).To(Succeed())
			Expect(call(client)).To(Succeed())
			Expect(fake.Requests()).To(HaveEach(HaveField("IfNoneMatch", "")))
			Expect(cache.Asked()).To(BeEmpty())
		},
		Entry("ListRuns by created range", func(c *github.HTTP) error {
			_, err := c.ListRuns(context.Background(), github.RunQuery{From: day, To: day.Add(24 * time.Hour)})
			return err
		}),
		Entry("GetAttempt", func(c *github.HTTP) error {
			_, _, err := c.GetAttempt(context.Background(), runID, 1)
			return err
		}),
		Entry("ListAttemptJobs", func(c *github.HTTP) error {
			_, _, err := c.ListAttemptJobs(context.Background(), runID, 1)
			return err
		}),
		Entry("CommitPulls", func(c *github.HTTP) error {
			_, _, err := c.CommitPulls(context.Background(), "1a51097dadb5b55978ac401b93f1ca9d8d317b02")
			return err
		}),
		Entry("DownloadJobLog", func(c *github.HTTP) error {
			return c.DownloadJobLog(context.Background(), 111221289888, &bytes.Buffer{})
		}),
	)

	It("reads a changed answer, and keeps it in place of the earlier one", func() {
		earlier, _, err := client.ListArtifacts(context.Background(), runID)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.Advance(runID, "after-attempt-3")).To(Succeed())
		before := len(fake.Requests())

		changed, _, err := client.ListArtifacts(context.Background(), runID)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).NotTo(Equal(earlier))
		Expect(since(before)[0]).To(SatisfyAll(HaveField("IfNoneMatch", Not(BeEmpty())), HaveField("Status", http.StatusOK)))

		again, _, err := client.ListArtifacts(context.Background(), runID)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(changed))
	})

	It("refuses a 304 to a GET that sent no If-None-Match", func() {
		fake.Fail("api", "/jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusNotModified})

		Expect(client.DownloadJobLog(context.Background(), 111221289888, &bytes.Buffer{})).To(MatchError(ContainSubstring("304")))
	})

	It("keeps no answer that has no ETag", func() {
		fake.Fail("api", repoURL, fakegithub.Fault{Status: http.StatusOK, Body: `{"full_name":"rosenhouse/Lg"}`, Times: 1})

		Expect(client.GetRepo(context.Background())).To(HaveField("FullName", "rosenhouse/Lg"))
		Expect(cache.Asked()).To(BeEmpty())
	})

	It("keeps the earlier answer when a GET fails", func() {
		Expect(client.GetRepo(context.Background())).Error().NotTo(HaveOccurred())
		fake.Fail("api", repoURL, fakegithub.Fault{Status: http.StatusBadGateway, Times: 1})
		Expect(client.GetRepo(context.Background())).Error().To(HaveOccurred())

		Expect(client.GetRepo(context.Background())).To(HaveField("DefaultBranch", "main"))
		Expect(fake.Requests()).To(HaveExactElements(
			HaveField("Status", http.StatusOK),
			HaveField("Status", http.StatusBadGateway),
			HaveField("Status", http.StatusNotModified),
		))
	})

	It("gives as Asked the answers it was asked for, not the other answers it was made with", func() {
		Expect(client.GetRepo(context.Background())).Error().NotTo(HaveOccurred())
		answers := cache.Asked()
		Expect(answers).To(HaveLen(1))
		answers[fake.URL()+"/repos/rosenhouse/lg/actions/runs/1"] = github.Answer{ETag: `"stale"`, Body: []byte(`{}`)}

		next := github.NewCache(answers)
		repo, err := github.NewHTTP(http.DefaultTransport, mustParse(fake.URL()), "rosenhouse/lg", "lg-test-token", clock.Real{}).WithCache(next).GetRepo(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(repo.DefaultBranch).To(Equal("main"))
		Expect(fake.Requests()[1]).To(HaveField("Status", http.StatusNotModified))
		Expect(next.Asked()).To(HaveKey(fake.URL() + repoURL))
		Expect(next.Asked()).To(HaveLen(1))
	})
})
