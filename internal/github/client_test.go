package github_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
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
		fake      *fakegithub.Server
		client    *github.HTTP
		recording string
	)

	BeforeEach(func() {
		fake = fakegithub.New()
		DeferCleanup(fake.Close)
		fake.Load(runID, "after-attempt-1")
		recording = fakegithub.Recording(runID, "after-attempt-1")
		client = github.NewHTTP(http.DefaultClient, fake.URL(), "rosenhouse/lg")
	})

	DescribeTable("sends Accept, X-GitHub-Api-Version and User-Agent lg/<version> on every request",
		func(call func(context.Context, *github.HTTP) error) {
			var headers http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers = r.Header
				_, _ = w.Write([]byte("{}"))
			}))
			DeferCleanup(server.Close)

			Expect(call(context.Background(), github.NewHTTP(http.DefaultClient, server.URL, "o/r"))).To(Succeed())
			Expect(headers.Get("Accept")).To(Equal("application/vnd.github+json"))
			Expect(headers.Get("X-GitHub-Api-Version")).To(Equal("2022-11-28"))
			Expect(headers.Get("User-Agent")).To(Equal("lg/" + version.Version))
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
		Expect(runs[0].ID).To(BeEquivalentTo(runID))
		Expect(runs[0].Repository.FullName).To(Equal("rosenhouse/Lg"))
		Expect(runs[0].Raw).To(MatchJSON(readFile(recording, "run.json")))
	})

	It("gets an attempt with its fields and the body served", func() {
		attempt, err := client.GetAttempt(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(attempt.RunAttempt).To(Equal(1))
		Expect(attempt.Raw).To(Equal(json.RawMessage(compact(readFile(recording, "attempt-1/attempt.json")))))
	})

	It("lists an attempt's jobs with their fields and the element served for each", func() {
		jobs, err := client.ListAttemptJobs(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())
		var listing struct{ Jobs []json.RawMessage }
		Expect(json.Unmarshal(readFile(recording, "attempt-1/jobs.json"), &listing)).To(Succeed())
		Expect(jobs).To(HaveLen(len(listing.Jobs)))
		for i, job := range jobs {
			Expect(job.Raw).To(Equal(json.RawMessage(compact(listing.Jobs[i]))))
		}
		Expect(jobs[10].Name).To(Equal("skipped"))
		Expect(jobs[10].RunnerName).To(BeNil())
	})

	It("downloads a job log through the redirect, byte for byte", func() {
		var log bytes.Buffer
		Expect(client.DownloadJobLog(context.Background(), 111221289888, &log)).To(Succeed())
		Expect(log.Bytes()).To(Equal(readFile(recording, "attempt-1/logs/111221289888.txt")))
	})

	It("returns an error naming the URL and status of a failed request", func() {
		_, err := client.GetAttempt(context.Background(), runID, 9)
		Expect(err).To(MatchError(fake.URL() + "/repos/rosenhouse/lg/actions/runs/37129390741/attempts/9: 404 Not Found"))
	})
})

func readFile(elem ...string) []byte {
	GinkgoHelper()
	b, err := os.ReadFile(filepath.Join(elem...))
	Expect(err).NotTo(HaveOccurred())
	return b
}

func compact(b []byte) []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(json.Compact(&buf, b)).To(Succeed())
	return buf.Bytes()
}
