package github_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/version"
)

var _ = Describe("BaseURL", Label("sync"), func() {
	DescribeTable("maps the host to its REST API root, unless api_url overrides it",
		func(host, apiURL, base string) {
			Expect(github.BaseURL(host, apiURL)).To(Equal(base))
		},
		Entry("github.com", "github.com", "", "https://api.github.com"),
		Entry("a GHES host", "ghe.corp.example", "", "https://ghe.corp.example/api/v3"),
		Entry("api_url for github.com", "github.com", "http://127.0.0.1:1/api/v3", "http://127.0.0.1:1/api/v3"),
		Entry("api_url for GHES", "ghe.corp.example", "http://127.0.0.1:1", "http://127.0.0.1:1"),
	)
})

var _ = Describe("HTTP client", Label("sync"), func() {
	const runID = 37129390741

	var (
		fake   *fakegithub.Server
		client *github.HTTP
	)

	BeforeEach(func() {
		fake = fakegithub.Start(runID, "after-attempt-1")
		client = github.NewHTTP(http.DefaultClient, fake.URL(), "rosenhouse/lg", "lg-test-token")
	})

	DescribeTable("sends Accept, X-GitHub-Api-Version, User-Agent lg/<version> and Authorization: Bearer <token> on every request",
		func(call func(context.Context, *github.HTTP) error) {
			var headers http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers = r.Header
				_, _ = w.Write([]byte(`{"total_count":0,"workflow_runs":[],"jobs":[]}`))
			}))
			DeferCleanup(server.Close)

			Expect(call(context.Background(), github.NewHTTP(http.DefaultClient, server.URL, "o/r", "lg-test-token"))).To(Succeed())
			Expect(headers.Get("Accept")).To(Equal("application/vnd.github+json"))
			Expect(headers.Get("X-GitHub-Api-Version")).To(Equal("2022-11-28"))
			Expect(headers.Get("User-Agent")).To(Equal("lg/" + version.Version))
			Expect(headers.Get("Authorization")).To(Equal("Bearer lg-test-token"))
		},
		Entry("ListRuns", func(ctx context.Context, c *github.HTTP) error { _, err := c.ListRuns(ctx); return err }),
		Entry("GetAttempt", func(ctx context.Context, c *github.HTTP) error { _, err := c.GetAttempt(ctx, 1, 1); return err }),
		Entry("ListAttemptJobs", func(ctx context.Context, c *github.HTTP) error { _, err := c.ListAttemptJobs(ctx, 1, 1); return err }),
		Entry("DownloadJobLog", func(ctx context.Context, c *github.HTTP) error { return c.DownloadJobLog(ctx, 1, &bytes.Buffer{}) }),
	)

	It("lists runs with their fields and the body served for each", func() {
		runs, err := client.ListRuns(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1))
		Expect(runs[0].Run).To(Equal(model.Run{
			ID:         runID,
			Name:       "lg-fixture",
			Path:       ".github/workflows/lg-fixture.yml",
			HeadBranch: "lg-fixture",
			CreatedAt:  time.Date(2026, 10, 3, 14, 22, 54, 0, time.UTC),
			Status:     "completed",
			RunAttempt: 1,
			Repository: model.Repository{FullName: "rosenhouse/Lg"},
		}))
		Expect(runs[0].Raw).To(MatchJSON(fake.Served("run.json")))
	})

	It("gets an attempt with its fields and the body served", func() {
		attempt, err := client.GetAttempt(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(attempt.RunAttempt).To(Equal(1))
		Expect(attempt.Raw).To(Equal(json.RawMessage(fake.Served("attempt-1/attempt.json"))))
	})

	It("lists an attempt's jobs with their fields and the element served for each", func() {
		jobs, err := client.ListAttemptJobs(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())
		var listing struct{ Jobs []json.RawMessage }
		Expect(json.Unmarshal(fake.Served("attempt-1/jobs.json"), &listing)).To(Succeed())
		Expect(jobs).To(HaveLen(len(listing.Jobs)))
		for i, job := range jobs {
			Expect(job.Raw).To(Equal(listing.Jobs[i]))
		}
		Expect(jobs[0].ID).To(BeEquivalentTo(111221289861))
		Expect(jobs[0].RunnerName).To(HaveValue(Equal("GitHub Actions 1000002376")))
		Expect(jobs[0].Steps).To(HaveLen(3))
		Expect(jobs[10].Name).To(Equal("skipped"))
		Expect(jobs[10].RunnerName).To(BeNil())
		Expect(jobs[10].Steps).To(BeEmpty())
	})

	It("downloads a job log through the redirect, byte for byte", func() {
		var log bytes.Buffer
		Expect(client.DownloadJobLog(context.Background(), 111221289888, &log)).To(Succeed())
		Expect(log.Bytes()).To(Equal(fake.Served("attempt-1/logs/111221289888.txt")))
	})

	It("sends no Authorization to a blob host on the API host's IP at another port", Label("transport"), func() {
		blobAuthorization := []string{}
		blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			blobAuthorization = append(blobAuthorization, r.Header.Get("Authorization"))
		}))
		DeferCleanup(blob.Close)
		api := httptest.NewServer(http.RedirectHandler(blob.URL+"/blob", http.StatusFound))
		DeferCleanup(api.Close)

		Expect(github.NewHTTP(http.DefaultClient, api.URL, "o/r", "lg-test-token").DownloadJobLog(context.Background(), 1, &bytes.Buffer{})).To(Succeed())
		Expect(blobAuthorization).To(Equal([]string{""}))
	})

	It("lists runs and jobs 100 per page", func() {
		_, err := client.ListRuns(context.Background())
		Expect(err).NotTo(HaveOccurred())
		_, err = client.ListAttemptJobs(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.Requests()).To(ConsistOf(
			HaveField("Query", "per_page=100"),
			HaveField("Query", "per_page=100"),
		))
	})

	It("follows Link next on the API host, joining the pages", Label("transport"), func() {
		Expect(fake.Load(37129738159, "logs-deleted")).To(Succeed())
		fake.SetPageCap(1)

		runs, err := client.ListRuns(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(2))
		jobs, err := client.ListAttemptJobs(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(jobs).To(HaveLen(12))
		Expect(fake.Requests()).To(HaveLen(14))
	})

	It("refuses a Link next to another host without requesting it", Label("transport"), func() {
		var foreign []string
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			foreign = append(foreign, r.URL.String())
		}))
		DeferCleanup(other.Close)
		next := other.URL + "/repositories/1/actions/runs?page=2"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Link", "<"+next+`>; rel="next"`)
			_, _ = w.Write([]byte(`{"total_count":2,"workflow_runs":[{"id":1}]}`))
		}))
		DeferCleanup(server.Close)

		_, err := github.NewHTTP(http.DefaultClient, server.URL, "o/r", "lg-test-token").ListRuns(context.Background())
		Expect(err).To(MatchError(server.URL + "/repos/o/r/actions/runs?per_page=100: Link next " + next + " is not on the API host"))
		Expect(foreign).To(BeEmpty())
	})

	It("refuses a Link next that repeats an earlier page without requesting it again", Label("transport"), func() {
		var requests int
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.Header().Set("Link", "<"+server.URL+r.URL.String()+`>; rel="next"`)
			_, _ = w.Write([]byte(`{"total_count":0,"workflow_runs":[]}`))
		}))
		DeferCleanup(server.Close)
		first := server.URL + "/repos/o/r/actions/runs?per_page=100"

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		DeferCleanup(cancel)
		_, err := github.NewHTTP(http.DefaultClient, server.URL, "o/r", "lg-test-token").ListRuns(ctx)
		Expect(err).To(MatchError(first + ": Link next " + first + " repeats an earlier page"))
		Expect(requests).To(Equal(1))
	})

	It("refuses a jobs listing whose total_count exceeds the jobs on its pages", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"total_count":2,"jobs":[{"id":1}]}`))
		}))
		DeferCleanup(server.Close)

		_, err := github.NewHTTP(http.DefaultClient, server.URL, "o/r", "lg-test-token").ListAttemptJobs(context.Background(), 1, 1)
		Expect(err).To(MatchError(server.URL + "/repos/o/r/actions/runs/1/attempts/1/jobs?per_page=100: listed 1 of 2 jobs"))
	})

	It("returns an error naming the URL of a truncated body", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("partial"))
		}))
		DeferCleanup(server.Close)

		err := github.NewHTTP(http.DefaultClient, server.URL, "o/r", "lg-test-token").DownloadJobLog(context.Background(), 1, &bytes.Buffer{})
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
		Expect(err).To(MatchError(HavePrefix(server.URL + "/repos/o/r/actions/jobs/1/logs: ")))
	})

	It("returns an error naming the URL and status of a failed request", func() {
		_, err := client.GetAttempt(context.Background(), runID, 9)
		Expect(err).To(MatchError(fake.URL() + "/repos/rosenhouse/lg/actions/runs/37129390741/attempts/9: 404 Not Found"))
	})
})
