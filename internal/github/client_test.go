package github_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/version"
)

var _ = Describe("BaseURL", Label("sync"), func() {
	DescribeTable("maps the host to its REST API root, unless api_url overrides it",
		func(host, apiURL, base string) {
			api, err := github.BaseURL(host, apiURL)
			Expect(err).NotTo(HaveOccurred())
			Expect(api.String()).To(Equal(base))
		},
		Entry("github.com", "github.com", "", "https://api.github.com"),
		Entry("a GHES host", "ghe.corp.example", "", "https://ghe.corp.example/api/v3"),
		Entry("api_url for github.com", "github.com", "http://127.0.0.1:1/api/v3", "http://127.0.0.1:1/api/v3"),
		Entry("api_url for GHES", "ghe.corp.example", "http://127.0.0.1:1", "http://127.0.0.1:1"),
		Entry("api_url with a trailing slash", "github.com", "http://127.0.0.1:1/api/v3/", "http://127.0.0.1:1/api/v3"),
		Entry("api_url of a bare host with a trailing slash", "github.com", "http://127.0.0.1:1/", "http://127.0.0.1:1"),
	)

	It("returns the error from parsing api_url", func() {
		_, err := github.BaseURL("github.com", "http://[::1")
		Expect(err).To(MatchError(ContainSubstring(`"http://[::1"`)))
	})
})

var _ = Describe("HTTP client", Label("sync"), func() {
	const runID = 37129390741

	var (
		fake   *fakegithub.Server
		client *github.HTTP
	)

	BeforeEach(func() {
		fake = fakegithub.Start(runID, "after-attempt-1")
		client = github.NewHTTP(http.DefaultTransport, mustParse(fake.URL()), "rosenhouse/lg", "lg-test-token", clock.Real{})
	})

	DescribeTable("sends Accept, X-GitHub-Api-Version, User-Agent lg/<version> and Authorization: Bearer <token> on every request", Label("transport"),
		func(call func(context.Context, *github.HTTP) error) {
			var headers http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers = r.Header
				_, _ = w.Write([]byte(`{"full_name":"o/r","total_count":0,"workflow_runs":[],"jobs":[],"artifacts":[],"status":"completed","updated_at":"2026-10-03T14:24:12Z","run_started_at":"2026-10-03T14:22:54Z","run_attempt":1}`))
			}))
			DeferCleanup(server.Close)

			Expect(call(context.Background(), github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "gho_header_test", clock.Real{}))).To(Succeed())
			Expect(headers.Get("Accept")).To(Equal("application/vnd.github+json"))
			Expect(headers.Get("X-GitHub-Api-Version")).To(Equal("2022-11-28"))
			Expect(headers.Get("User-Agent")).To(Equal("lg/" + version.Version))
			Expect(headers.Get("Authorization")).To(Equal("Bearer gho_header_test"))
		},
		Entry("GetRepo", func(ctx context.Context, c *github.HTTP) error { _, err := c.GetRepo(ctx); return err }),
		Entry("ListRuns", func(ctx context.Context, c *github.HTTP) error { _, err := c.ListRuns(ctx); return err }),
		Entry("GetRun", Label("artifacts"), func(ctx context.Context, c *github.HTTP) error { _, err := c.GetRun(ctx, 1); return err }),
		Entry("GetAttempt", func(ctx context.Context, c *github.HTTP) error { _, _, err := c.GetAttempt(ctx, 1, 1); return err }),
		Entry("ListAttemptJobs", func(ctx context.Context, c *github.HTTP) error { _, _, err := c.ListAttemptJobs(ctx, 1, 1); return err }),
		Entry("DownloadJobLog", func(ctx context.Context, c *github.HTTP) error { return c.DownloadJobLog(ctx, 1, &bytes.Buffer{}) }),
		Entry("ListArtifacts", Label("artifacts"), func(ctx context.Context, c *github.HTTP) error { _, _, err := c.ListArtifacts(ctx, 1); return err }),
		Entry("DownloadArtifact", Label("artifacts"), func(ctx context.Context, c *github.HTTP) error { return c.DownloadArtifact(ctx, 1, &bytes.Buffer{}) }),
	)

	It("lists runs with their fields and the body served for each", func() {
		runs, err := client.ListRuns(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1))
		Expect(runs[0].Run).To(Equal(model.Run{
			ID:           runID,
			Name:         "lg-fixture",
			Path:         ".github/workflows/lg-fixture.yml",
			HeadBranch:   "lg-fixture",
			CreatedAt:    time.Date(2026, 10, 3, 14, 22, 54, 0, time.UTC),
			RunStartedAt: time.Date(2026, 10, 3, 14, 22, 54, 0, time.UTC),
			UpdatedAt:    time.Date(2026, 10, 3, 14, 24, 12, 0, time.UTC),
			Status:       "completed",
			RunAttempt:   1,
			Repository:   model.Repository{FullName: "rosenhouse/Lg"},
			WorkflowID:   373958224,
			Event:        "push",
			PullRequests: []model.PullRequest{},
			DisplayTitle: "Add lg-fixture workflow for recording Actions API shapes",
		}))
		Expect(runs[0].Raw).To(MatchJSON(fake.Served("run.json")))
	})

	It("gets a run with its fields and the body served", Label("artifacts"), func() {
		run, err := client.GetRun(context.Background(), runID)
		Expect(err).NotTo(HaveOccurred())
		Expect(run.RunAttempt).To(Equal(1))
		Expect(run.Raw).To(Equal(json.RawMessage(fake.Served("run.json"))))
	})

	It("maps a 404 on GetRun to ErrNotFound", Label("artifacts"), func() {
		_, err := client.GetRun(context.Background(), 9)
		Expect(err).To(MatchError(github.ErrNotFound))
	})

	It("calls a run with a field of the wrong type malformed, naming its URL", Label("artifacts"), func() {
		fake.Fail("api", "runs/37129390741", fakegithub.Fault{Status: http.StatusOK, Body: `{"id":"x"}`})
		_, err := client.GetRun(context.Background(), runID)
		var malformed *github.MalformedError
		Expect(errors.As(err, &malformed)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("/actions/runs/37129390741")))
	})

	It("gets an attempt with its fields and the body served", func() {
		attempt, _, err := client.GetAttempt(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(attempt.RunAttempt).To(Equal(1))
		Expect(attempt.Raw).To(Equal(json.RawMessage(fake.Served("attempt-1/attempt.json"))))
	})

	It("lists an attempt's jobs with their fields and the element served for each", func() {
		jobs, _, err := client.ListAttemptJobs(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())
		var listing struct{ Jobs []json.RawMessage }
		Expect(json.Unmarshal(fake.Served("attempt-1/jobs.json"), &listing)).To(Succeed())
		Expect(jobs).To(HaveLen(len(listing.Jobs)))
		for i, job := range jobs {
			Expect(job.Raw).To(Equal(listing.Jobs[i]))
		}
		Expect(jobs[0].ID).To(BeEquivalentTo(111221289861))
		Expect(jobs[0].StartedAt).To(HaveValue(Equal(time.Date(2026, 10, 3, 14, 22, 57, 0, time.UTC))))
		Expect(jobs[0].RunnerName).To(HaveValue(Equal("GitHub Actions 1000002376")))
		Expect(jobs[0].Steps).To(HaveLen(3))
		Expect(jobs[10].Name).To(Equal("skipped"))
		Expect(jobs[10].RunnerName).To(BeNil())
		Expect(jobs[10].Steps).To(BeEmpty())
	})

	It("gives the API URL, status and page count of what it fetched", Label("failures"), func() {
		fake.SetPageCap(5)
		_, attempt, err := client.GetAttempt(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())
		_, listing, err := client.ListAttemptJobs(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())

		api := fake.URL() + "/repos/rosenhouse/lg/actions/"
		Expect(attempt).To(Equal(github.Source{URL: api + "runs/37129390741/attempts/1"}))
		Expect(listing).To(Equal(github.Source{URL: api + "runs/37129390741/attempts/1/jobs?per_page=100", Pages: 3}))
		Expect(client.JobLogURL(111221289888)).To(Equal(api + "jobs/111221289888/logs"))
	})

	It("downloads a job log through the redirect, byte for byte", func() {
		var log bytes.Buffer
		Expect(client.DownloadJobLog(context.Background(), 111221289888, &log)).To(Succeed())
		Expect(log.Bytes()).To(Equal(fake.Served("attempt-1/logs/111221289888.txt")))
	})

	It("lists a run's artifacts on every page, with their fields, the element served for each, and the listing's URL and page count", Label("artifacts"), func() {
		fake.SetPageCap(3)
		artifacts, source, err := client.ListArtifacts(context.Background(), runID)
		Expect(err).NotTo(HaveOccurred())

		var listing struct{ Artifacts []json.RawMessage }
		Expect(json.Unmarshal(fake.Served("artifacts.json"), &listing)).To(Succeed())
		Expect(artifacts).To(HaveLen(4))
		for i, artifact := range artifacts {
			Expect(artifact.Raw).To(Equal(listing.Artifacts[i]))
		}
		Expect(artifacts[1].Artifact).To(Equal(model.Artifact{
			ID:          11276272069,
			Name:        "pass-artifact",
			SizeInBytes: 751,
			CreatedAt:   time.Date(2026, 10, 3, 14, 23, 1, 0, time.UTC),
			ExpiresAt:   time.Date(2027, 1, 1, 14, 22, 54, 0, time.UTC),
			Digest:      "sha256:9b47ee49e71ab033f37453c4d1ffb4cb5c1608046721a8bcb15f5d1d70508a61",
		}))
		Expect(source).To(Equal(github.Source{URL: fake.URL() + "/repos/rosenhouse/lg/actions/runs/37129390741/artifacts?per_page=100", Pages: 2}))
	})

	It("downloads an artifact's zip through the redirect, byte for byte", Label("artifacts"), func() {
		var zip bytes.Buffer
		Expect(client.DownloadArtifact(context.Background(), 11276272069, &zip)).To(Succeed())
		Expect(zip.Bytes()).To(Equal(fake.Served("artifacts/11276272069.zip")))
		Expect(client.ArtifactZipURL(11276272069)).To(Equal(fake.URL() + "/repos/rosenhouse/lg/actions/artifacts/11276272069/zip"))
	})

	It("sends no Authorization to a blob host on the API host's IP at another port", Label("transport"), func() {
		blobAuthorization := []string{}
		blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			blobAuthorization = append(blobAuthorization, r.Header.Get("Authorization"))
		}))
		DeferCleanup(blob.Close)
		api := httptest.NewServer(http.RedirectHandler(blob.URL+"/blob", http.StatusFound))
		DeferCleanup(api.Close)

		Expect(github.NewHTTP(http.DefaultTransport, mustParse(api.URL), "o/r", "lg-test-token", clock.Real{}).DownloadJobLog(context.Background(), 1, &bytes.Buffer{})).To(Succeed())
		Expect(blobAuthorization).To(Equal([]string{""}))
	})

	It("stops after 10 redirects", Label("transport"), func(ctx SpecContext) {
		var requests int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			http.Redirect(w, r, r.URL.String(), http.StatusFound)
		}))
		DeferCleanup(server.Close)

		err := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{}).DownloadJobLog(ctx, 1, &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("stopped after 10 redirects")))
		Expect(requests).To(Equal(10))
	}, SpecTimeout(5*time.Second))

	It("lists runs and jobs 100 per page", func() {
		_, err := client.ListRuns(context.Background())
		Expect(err).NotTo(HaveOccurred())
		_, _, err = client.ListAttemptJobs(context.Background(), runID, 1)
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
		jobs, _, err := client.ListAttemptJobs(context.Background(), runID, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(jobs).To(HaveLen(12))
		requested := func(path string, page int) types.GomegaMatcher {
			return SatisfyAll(HaveField("Path", path), HaveField("Query", fmt.Sprintf("page=%d&per_page=100", page)))
		}
		want := []any{
			HaveField("Path", "/repos/rosenhouse/lg/actions/runs"),
			requested("/repositories/1402714635/actions/runs", 2),
			HaveField("Path", "/repos/rosenhouse/lg/actions/runs/37129390741/attempts/1/jobs"),
		}
		for page := 2; page <= 12; page++ {
			want = append(want, requested("/repositories/1402714635/actions/runs/37129390741/attempts/1/jobs", page))
		}
		Expect(fake.Requests()).To(HaveExactElements(want...))
	})

	DescribeTable("follows Link next only on the API host's scheme, host and port", Label("transport"),
		func(apiURL, next string, onAPIHost bool) {
			var requests []string
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.String())
				if len(requests) == 1 {
					w.Header().Set("Link", "<"+next+`>; rel="next"`)
				}
				_, _ = w.Write([]byte(`{"total_count":2,"workflow_runs":[{"id":1}]}`))
			}))
			if strings.HasPrefix(apiURL, "https:") {
				server.StartTLS()
			} else {
				server.Start()
			}
			DeferCleanup(server.Close)
			roots := x509.NewCertPool()
			if server.Certificate() != nil {
				roots.AddCert(server.Certificate())
			}
			everyHostIsServer := &http.Transport{
				DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				},
				// httptest's certificate is for example.com.
				TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12},
			}

			runs, err := github.NewHTTP(everyHostIsServer, mustParse(apiURL), "o/r", "lg-test-token", clock.Real{}).ListRuns(context.Background())

			if onAPIHost {
				Expect(err).NotTo(HaveOccurred())
				Expect(runs).To(HaveLen(2))
				Expect(requests).To(Equal([]string{"/repos/o/r/actions/runs?per_page=100", "/repositories/1/actions/runs?page=2"}))
			} else {
				Expect(err).To(MatchError(apiURL + "/repos/o/r/actions/runs?per_page=100: Link next " + next + " is not on the API host"))
				Expect(requests).To(HaveLen(1))
			}
		},
		Entry("the same host", "http://api.example", "http://api.example/repositories/1/actions/runs?page=2", true),
		Entry("api_url in upper case", "http://API.EXAMPLE", "http://api.example/repositories/1/actions/runs?page=2", true),
		Entry("api_url with the default port", "http://api.example:80", "http://api.example/repositories/1/actions/runs?page=2", true),
		Entry("the Link with the default port", "http://api.example", "http://api.example:80/repositories/1/actions/runs?page=2", true),
		Entry("https api_url with the default port", "https://api.example:443", "https://api.example/repositories/1/actions/runs?page=2", true),
		Entry("the https Link with the default port", "https://api.example", "https://api.example:443/repositories/1/actions/runs?page=2", true),
		Entry("another port", "http://api.example", "http://api.example:8080/repositories/1/actions/runs?page=2", false),
		Entry("another scheme", "http://api.example:8443", "https://api.example:8443/repositories/1/actions/runs?page=2", false),
		Entry("another host", "http://api.example", "http://blob.example/repositories/1/actions/runs?page=2", false),
		Entry("a subdomain", "http://api.example", "http://blob.api.example/repositories/1/actions/runs?page=2", false),
	)

	It("follows the URL marked rel=\"next\" when Link lists prev first", Label("transport"), func() {
		var requests []string
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.URL.String())
			page := func(n int) string { return fmt.Sprintf("<%s/repositories/1/actions/runs?page=%d>", server.URL, n) }
			switch len(requests) {
			case 1:
				w.Header().Set("Link", page(2)+`; rel="next", `+page(3)+`; rel="last"`)
			case 2:
				w.Header().Set("Link", page(1)+`; rel="prev", `+page(3)+`; rel="next", `+page(3)+`; rel="last", `+page(1)+`; rel="first"`)
			case 3:
				w.Header().Set("Link", page(2)+`; rel="prev", `+page(1)+`; rel="first"`)
			}
			_, _ = w.Write([]byte(`{"total_count":3,"workflow_runs":[{"id":1}]}`))
		}))
		DeferCleanup(server.Close)

		runs, err := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{}).ListRuns(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(3))
		Expect(requests).To(Equal([]string{
			"/repos/o/r/actions/runs?per_page=100",
			"/repositories/1/actions/runs?page=2",
			"/repositories/1/actions/runs?page=3",
		}))
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
		_, err := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{}).ListRuns(ctx)
		Expect(err).To(MatchError(first + ": Link next " + first + " repeats an earlier page"))
		Expect(requests).To(Equal(1))
	})

	It("refuses a jobs listing whose total_count exceeds the jobs on its pages", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"total_count":2,"jobs":[{"id":1}]}`))
		}))
		DeferCleanup(server.Close)

		_, _, err := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{}).ListAttemptJobs(context.Background(), 1, 1)
		Expect(err).To(MatchError(server.URL + "/repos/o/r/actions/runs/1/attempts/1/jobs?per_page=100: listed 1 of 2 jobs"))
	})

	DescribeTable("refuses an artifacts listing as malformed", Label("artifacts"),
		func(body, reason string) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			DeferCleanup(server.Close)

			_, _, err := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{}).ListArtifacts(context.Background(), 1)
			var malformed *github.MalformedError
			Expect(errors.As(err, &malformed)).To(BeTrue())
			Expect(err).To(MatchError(server.URL + "/repos/o/r/actions/runs/1/artifacts?per_page=100: " + reason))
		},
		Entry("when total_count exceeds the artifacts on its pages", `{"total_count":2,"artifacts":[{"id":1}]}`, "listed 1 of 2 artifacts"),
		Entry("when it repeats an artifact id", `{"total_count":2,"artifacts":[{"id":5},{"id":5}]}`, "artifact 5 listed twice"),
		Entry("when an artifact has no id", `{"total_count":2,"artifacts":[{"id":5},{"name":"a"}]}`, "artifact #1 has no id"),
		Entry("when an artifact has no created_at", `{"total_count":1,"artifacts":[{"id":5,"expires_at":"2026-10-04T14:23:01Z"}]}`, "artifact 5 has no created_at"),
		Entry("when an artifact has a null created_at", `{"total_count":1,"artifacts":[{"id":5,"created_at":null}]}`, "artifact 5 has no created_at"),
	)

	It("calls an artifacts listing with an unparsable element malformed, naming its URL", Label("artifacts"), func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"total_count":1,"artifacts":[{"id":"1"}]}`))
		}))
		DeferCleanup(server.Close)

		_, _, err := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{}).ListArtifacts(context.Background(), 1)
		var malformed *github.MalformedError
		Expect(errors.As(err, &malformed)).To(BeTrue())
		Expect(err).To(MatchError(HavePrefix(server.URL + "/repos/o/r/actions/runs/1/artifacts?per_page=100: ")))
	})

	It("returns an error naming the URL of a truncated body", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("partial"))
		}))
		DeferCleanup(server.Close)

		err := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{}).DownloadJobLog(context.Background(), 1, &bytes.Buffer{})
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
		Expect(err).To(MatchError(HavePrefix(server.URL + "/repos/o/r/actions/jobs/1/logs: ")))
	})

	It("returns an error naming the URL and status of a failed request", func() {
		_, _, err := client.GetAttempt(context.Background(), runID, 9)
		Expect(err).To(MatchError(fake.URL() + "/repos/rosenhouse/lg/actions/runs/37129390741/attempts/9: 404 Not Found"))
	})
})

func mustParse(rawURL string) *url.URL {
	GinkgoHelper()
	u, err := url.Parse(rawURL)
	Expect(err).NotTo(HaveOccurred())
	return u
}
