package github_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/github"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
)

var hopTimeout = SpecTimeout(5 * time.Second)

var shortTimeouts = github.Timeouts{Dial: time.Second, TLSHandshake: time.Second, ResponseHeader: 200 * time.Millisecond, BodyIdle: 200 * time.Millisecond}

const blobSignature = "lg-test-secret"

// hops serves the log of job 1 as GitHub does: the API redirects to a blob
// host, which answers blob.
func hops(api, blob http.HandlerFunc) *github.HTTP {
	GinkgoHelper()
	blobHost := httptest.NewServer(blob)
	DeferCleanup(blobHost.Close)
	if api == nil {
		api = func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, blobHost.URL+"/log?sig="+blobSignature, http.StatusFound)
		}
	}
	apiHost := httptest.NewServer(api)
	DeferCleanup(apiHost.Close)
	return github.NewHTTP(github.NewTransport(shortTimeouts), mustParse(apiHost.URL), "o/r", "lg-test-token")
}

func answer(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// stall writes body, then waits until the client gives up.
func stall(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if body != "" {
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte(body))
			_ = http.NewResponseController(w).Flush()
		}
		<-r.Context().Done()
	}
}

func getAttempt(ctx context.Context, client *github.HTTP) error {
	_, _, err := client.GetAttempt(ctx, 1, 1)
	return err
}

func listJobs(ctx context.Context, client *github.HTTP) error {
	_, _, err := client.ListAttemptJobs(ctx, 1, 1)
	return err
}

// linking answers an empty jobs page whose Link next is next.
func linking(next string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", "<"+next+`>; rel="next"`)
		_, _ = w.Write([]byte(`{"total_count":0,"jobs":[]}`))
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

// cancelOnWrite cancels once the body has started.
type cancelOnWrite context.CancelFunc

func (c cancelOnWrite) Write(p []byte) (int, error) {
	c()
	return len(p), nil
}

const (
	gitHubNotFound = `{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}`
	blobNotFound   = "\uFEFF<?xml version=\"1.0\" encoding=\"utf-8\"?><Error><Code>BlobNotFound</Code><Message>The specified blob does not exist.\nRequestId:b712d84b\nTime:2026-10-03T14:24:46.3969794Z</Message></Error>"
)

var _ = Describe("HTTP errors", Label("failures"), func() {
	DescribeTable("classifies a failed download by hop",
		func(ctx SpecContext, api, blob http.HandlerFunc, kind types.GomegaMatcher) {
			Expect(hops(api, blob).DownloadJobLog(ctx, 1, &bytes.Buffer{})).To(kind)
		},
		Entry("an API 404 is ErrNotFound", answer(http.StatusNotFound, gitHubNotFound), nil, MatchError(github.ErrNotFound), hopTimeout),
		Entry("a blob 404 is ErrBlobMissing", nil, answer(http.StatusNotFound, blobNotFound), MatchError(github.ErrBlobMissing), hopTimeout),
		Entry("an API 410 is ErrGone", answer(http.StatusGone, `{"message":"Gone"}`), nil, MatchError(github.ErrGone), hopTimeout),
		Entry("a blob 410 is ErrGone", nil, answer(http.StatusGone, ""), MatchError(github.ErrGone), hopTimeout),
		Entry("an API 500 is Transient", answer(http.StatusInternalServerError, ""), nil, BeTransient(), hopTimeout),
		Entry("an API 502 is Transient", answer(http.StatusBadGateway, ""), nil, BeTransient(), hopTimeout),
		Entry("a blob 401 is Transient", nil, answer(http.StatusUnauthorized, ""), BeTransient(), hopTimeout),
		Entry("a blob 403 is Transient", nil, answer(http.StatusForbidden, ""), BeTransient(), hopTimeout),
		Entry("a blob 429 is Transient", nil, answer(http.StatusTooManyRequests, ""), BeTransient(), hopTimeout),
		Entry("a blob 503 is Transient", nil, answer(http.StatusServiceUnavailable, ""), BeTransient(), hopTimeout),
		Entry("a short body is Transient", nil, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("partial"))
		}, BeTransient(), hopTimeout),
		Entry("stalled headers are Transient", stall(""), nil, BeTransient(), hopTimeout),
		Entry("a stalled body is Transient", nil, stall("partial"), BeTransient(), hopTimeout),
	)

	It("keeps an API 404 apart from ErrBlobMissing", func(ctx SpecContext) {
		err := hops(answer(http.StatusNotFound, gitHubNotFound), nil).DownloadJobLog(ctx, 1, &bytes.Buffer{})
		Expect(err).NotTo(MatchError(github.ErrBlobMissing))
	})

	It("keeps a blob 404 apart from ErrNotFound", func(ctx SpecContext) {
		err := hops(nil, answer(http.StatusNotFound, blobNotFound)).DownloadJobLog(ctx, 1, &bytes.Buffer{})
		Expect(err).NotTo(MatchError(github.ErrNotFound))
	})

	It("keeps a blob 403 apart from ErrNotFound and ErrBlobMissing", func(ctx SpecContext) {
		err := hops(nil, answer(http.StatusForbidden, "")).DownloadJobLog(ctx, 1, &bytes.Buffer{})
		Expect(err).NotTo(MatchError(github.ErrNotFound))
		Expect(err).NotTo(MatchError(github.ErrBlobMissing))
	})

	DescribeTable("leaves an API error that is not a 5xx neither Transient nor a gap",
		func(ctx SpecContext, status int) {
			err := hops(answer(status, ""), nil).DownloadJobLog(ctx, 1, &bytes.Buffer{})
			Expect(err).To(MatchError(ContainSubstring(strconv.Itoa(status))))
			Expect(err).NotTo(BeTransient())
			Expect(err).NotTo(MatchError(github.ErrNotFound))
			Expect(err).NotTo(MatchError(github.ErrGone))
		},
		Entry("401", http.StatusUnauthorized, hopTimeout),
		Entry("403", http.StatusForbidden, hopTimeout),
		Entry("422", http.StatusUnprocessableEntity, hopTimeout),
	)

	It("leaves a failed write to w neither Transient nor a gap", func(ctx SpecContext) {
		err := hops(nil, answer(http.StatusOK, "log")).DownloadJobLog(ctx, 1, failingWriter{syscall.ENOSPC})
		Expect(err).To(MatchError(syscall.ENOSPC))
		Expect(err).NotTo(BeTransient())
		var malformed *github.MalformedError
		Expect(errors.As(err, &malformed)).To(BeFalse())
	}, hopTimeout)

	It("leaves a malformed 200 body non-Transient", func(ctx SpecContext) {
		_, _, err := hops(answer(http.StatusOK, "{"), nil).GetAttempt(ctx, 1, 1)
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
		Expect(err).NotTo(BeTransient())
	}, hopTimeout)

	DescribeTable("calls a 200 that lg cannot use a MalformedError naming its API URL",
		func(ctx SpecContext, api http.HandlerFunc, get func(context.Context, *github.HTTP) error) {
			err := get(ctx, hops(api, nil))
			var malformed *github.MalformedError
			Expect(errors.As(err, &malformed)).To(BeTrue(), "%v", err)
			Expect(err).To(MatchError(MatchRegexp(`^http://127\.0\.0\.1:\d+/repos/o/r/actions/runs/1/attempts/1\S*: `)))
			Expect(err).NotTo(BeTransient())
		},
		Entry("an attempt that is not JSON", answer(http.StatusOK, "<html>"), getAttempt, hopTimeout),
		Entry("an attempt with a field of the wrong type", answer(http.StatusOK, `{"id":"x"}`), getAttempt, hopTimeout),
		Entry("an attempt with no status", answer(http.StatusOK, `{"updated_at":"2026-10-03T14:24:12Z","run_attempt":1}`), getAttempt, hopTimeout),
		Entry("an attempt with no updated_at", answer(http.StatusOK, `{"status":"completed","run_attempt":1}`), getAttempt, hopTimeout),
		Entry("an attempt numbered other than requested", answer(http.StatusOK, `{"status":"completed","updated_at":"2026-10-03T14:24:12Z","run_attempt":2}`), getAttempt, hopTimeout),
		Entry("jobs that are not JSON", answer(http.StatusOK, "<html>"), listJobs, hopTimeout),
		Entry("jobs that are not an array", answer(http.StatusOK, `{"total_count":1,"jobs":{}}`), listJobs, hopTimeout),
		Entry("a job with a field of the wrong type", answer(http.StatusOK, `{"total_count":1,"jobs":[{"id":"x"}]}`), listJobs, hopTimeout),
		Entry("jobs short of their total_count", answer(http.StatusOK, `{"total_count":2,"jobs":[{"id":1}]}`), listJobs, hopTimeout),
		Entry("a Link next off the API host", linking("http://other.example/next"), listJobs, hopTimeout),
		Entry("a Link next that repeats a page", func(w http.ResponseWriter, r *http.Request) {
			linking("http://"+r.Host+r.URL.RequestURI())(w, r)
		}, listJobs, hopTimeout),
	)

	DescribeTable("names the URL and the missing field of a jobs listing, in one line",
		func(ctx SpecContext, body, field string) {
			_, _, err := hops(answer(http.StatusOK, body), nil).ListAttemptJobs(ctx, 1, 1)
			Expect(err).To(MatchError(MatchRegexp(`^http://127\.0\.0\.1:\d+/repos/o/r/actions/runs/1/attempts/1/jobs\?per_page=100: no "%s"$`, field)))
		},
		Entry("no total_count", `{"jobs":[]}`, "total_count", hopTimeout),
		Entry("no jobs", `{"total_count":0}`, "jobs", hopTimeout),
		Entry("neither", `{"message":"OK"}`, "total_count", hopTimeout),
	)

	DescribeTable("gives the API URL, the status and the message of a failed hop",
		func(ctx SpecContext, api, blob http.HandlerFunc, status int, message string) {
			client := hops(api, blob)
			err := client.DownloadJobLog(ctx, 1, &bytes.Buffer{})
			var statusErr *github.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue())
			Expect(statusErr.URL).To(MatchRegexp(`^http://127\.0\.0\.1:\d+/repos/o/r/actions/jobs/1/logs$`))
			Expect(statusErr.Status).To(Equal(status))
			Expect(statusErr.Message).To(Equal(message))
		},
		Entry("GitHub's JSON message", answer(http.StatusNotFound, gitHubNotFound), nil, 404, "Not Found"),
		Entry("blob storage's XML message, first line", nil, answer(http.StatusNotFound, blobNotFound), 404, "The specified blob does not exist."),
		Entry("the status text of a body with no message", answer(http.StatusGone, "gone"), nil, 410, "Gone"),
	)

	It("never names the blob URL, whose query is a credential", func(ctx SpecContext) {
		client := hops(nil, func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := http.NewResponseController(w).Hijack()
			Expect(err).NotTo(HaveOccurred())
			_ = conn.Close()
		})

		err := client.DownloadJobLog(ctx, 1, &bytes.Buffer{})
		Expect(err).To(BeTransient())
		Expect(err.Error()).To(MatchRegexp(`^http://127\.0\.0\.1:\d+/repos/o/r/actions/jobs/1/logs: `))
		Expect(err.Error()).NotTo(ContainSubstring(blobSignature))
	})

	DescribeTable("follows a redirect of any kind to the log",
		func(ctx SpecContext, status int) {
			var blobURL string
			client := hops(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, blobURL, status)
			}, nil)
			blob := httptest.NewServer(answer(http.StatusOK, "log"))
			DeferCleanup(blob.Close)
			blobURL = blob.URL + "/log"

			var log bytes.Buffer
			Expect(client.DownloadJobLog(ctx, 1, &log)).To(Succeed())
			Expect(log.String()).To(Equal("log"))
		},
		Entry("301", http.StatusMovedPermanently, hopTimeout),
		Entry("302", http.StatusFound, hopTimeout),
		Entry("303", http.StatusSeeOther, hopTimeout),
		Entry("307", http.StatusTemporaryRedirect, hopTimeout),
		Entry("308", http.StatusPermanentRedirect, hopTimeout),
	)

	It("gives a StatusError for a redirect with no Location", func(ctx SpecContext) {
		err := hops(answer(http.StatusFound, ""), nil).DownloadJobLog(ctx, 1, &bytes.Buffer{})
		var statusErr *github.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue())
		Expect(statusErr.Status).To(Equal(http.StatusFound))
	}, hopTimeout)

	DescribeTable("never names a redirect Location it cannot parse",
		func(ctx SpecContext, location string) {
			client := hops(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusFound)
			}, nil)

			err := client.DownloadJobLog(ctx, 1, &bytes.Buffer{})
			Expect(err).To(MatchError(MatchRegexp(`^http://127\.0\.0\.1:\d+/repos/o/r/actions/jobs/1/logs: unparsable redirect Location$`)))
		},
		Entry("a space in the host", "http://bad host/log?sig="+blobSignature, hopTimeout),
		Entry("a bad escape in the path", "http://localhost:1/log%zz?sig="+blobSignature, hopTimeout),
	)

	DescribeTable("leaves a parent's cancellation non-Transient",
		func(cancelled func(cancel context.CancelFunc) (api, blob http.HandlerFunc, w io.Writer)) {
			ctx, cancel := context.WithCancel(context.Background())
			api, blob, w := cancelled(cancel)

			err := hops(api, blob).DownloadJobLog(ctx, 1, w)
			Expect(err).To(MatchError(context.Canceled))
			Expect(err).NotTo(BeTransient())
		},
		Entry("before the response", func(cancel context.CancelFunc) (http.HandlerFunc, http.HandlerFunc, io.Writer) {
			return func(_ http.ResponseWriter, r *http.Request) {
				cancel()
				<-r.Context().Done()
			}, nil, &bytes.Buffer{}
		}),
		Entry("mid-body", func(cancel context.CancelFunc) (http.HandlerFunc, http.HandlerFunc, io.Writer) {
			return nil, stall("partial"), cancelOnWrite(cancel)
		}),
	)
})

var _ = Describe("DefaultTimeouts", Label("failures"), func() {
	It("waits 10s to dial, 10s for a TLS handshake, 30s for headers and 60s on an idle body", func() {
		Expect(github.DefaultTimeouts()).To(Equal(github.Timeouts{Dial: 10 * time.Second, TLSHandshake: 10 * time.Second, ResponseHeader: 30 * time.Second, BodyIdle: time.Minute}))
	})

	It("are NewDefault's", func() {
		Expect(github.TimeoutsOf(github.NewDefault(mustParse("https://api.github.com"), "o/r", "lg-test-token"))).To(Equal(github.DefaultTimeouts()))
	})
})

var _ = Describe("NewTransport", Label("failures"), func() {
	It("uses the proxy from the environment", func() {
		Expect(github.TransportOf(github.NewTransport(github.DefaultTimeouts())).Proxy).NotTo(BeNil())
	})

	It("gives up on a body idle for BodyIdle", func(ctx SpecContext) {
		client := hops(nil, stall("partial"))

		start := clock.Real{}.Now()
		err := client.DownloadJobLog(ctx, 1, &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("idle for 200ms")))
		Expect(clock.Real{}.Now()).To(BeTemporally("<", start.Add(2*time.Second)))
	}, SpecTimeout(5*time.Second))

	It("reads a body that trickles in for longer than BodyIdle", func(ctx SpecContext) {
		client := hops(nil, func(w http.ResponseWriter, _ *http.Request) {
			for range 6 {
				_, _ = w.Write([]byte("x"))
				_ = http.NewResponseController(w).Flush()
				<-clock.Real{}.After(100 * time.Millisecond)
			}
		})

		var log bytes.Buffer
		err := client.DownloadJobLog(ctx, 1, &log)
		Expect(err).NotTo(HaveOccurred())
		Expect(log.String()).To(Equal("xxxxxx"))
	}, SpecTimeout(5*time.Second))

	It("gives up on a dial after Dial", func(ctx SpecContext) {
		server := httptest.NewServer(answer(http.StatusOK, "{}"))
		DeferCleanup(server.Close)
		timeouts := shortTimeouts
		timeouts.Dial = time.Nanosecond
		client := github.NewHTTP(github.NewTransport(timeouts), mustParse(server.URL), "o/r", "lg-test-token")

		_, _, err := client.GetAttempt(ctx, 1, 1)
		Expect(err).To(MatchError(ContainSubstring("dial tcp 127.0.0.1:")))
		Expect(err).To(MatchError(ContainSubstring("i/o timeout")))
	}, SpecTimeout(5*time.Second))

	It("gives up on a TLS handshake after TLSHandshake", func(ctx SpecContext) {
		// The kernel completes the TCP handshake; nothing answers the TLS one.
		silent, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(silent.Close)
		timeouts := shortTimeouts
		timeouts.TLSHandshake = 200 * time.Millisecond
		client := github.NewHTTP(github.NewTransport(timeouts), mustParse("https://"+silent.Addr().String()), "o/r", "lg-test-token")

		_, _, err = client.GetAttempt(ctx, 1, 1)
		Expect(err).To(MatchError(ContainSubstring("TLS handshake timeout")))
		Expect(err).To(BeTransient())
	}, SpecTimeout(5*time.Second))
})
