package github_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
)

var start = time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)

func serve(h http.HandlerFunc) *httptest.Server {
	server := httptest.NewServer(h)
	DeferCleanup(server.Close)
	return server
}

// counting serves h and counts the requests it serves.
func counting(h http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	var served atomic.Int32
	server := serve(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		h(w, r)
	})
	return server, &served
}

const attemptBody = `{"status":"completed","updated_at":"2026-10-03T14:24:12Z","run_attempt":1}`

var _ = Describe("HTTP errors that block the cycle", Label("blocked"), func() {
	It("give the API URL, status and message of an API response that refuses the token", func(ctx SpecContext) {
		client := hops(answer(http.StatusUnauthorized, `{"message":"Bad credentials"}`), nil)

		Expect(client.DownloadJobLog(ctx, 1, &bytes.Buffer{})).To(BeBlocked(failure.Auth,
			HaveField("Detail", MatchRegexp(`^http://127\.0\.0\.1:\d+/repos/o/r/actions/jobs/1/logs: 401 Unauthorized: Bad credentials$`)),
		))
	}, hopTimeout)

	It("take retry_at from the client's clock", func(ctx SpecContext) {
		server := serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		})
		client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.NewFake(start))

		Expect(getAttempt(ctx, client)).To(BeBlocked(failure.RateLimit,
			HaveField("RetryAt", start.Add(30*time.Second)),
		))
	}, hopTimeout)

	It("call a refused dial unreachable, naming the host", func(ctx SpecContext) {
		server := httptest.NewServer(answer(http.StatusOK, attemptBody))
		server.Close()
		client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{})

		Expect(getAttempt(ctx, client)).To(BeBlocked(failure.Unreachable,
			HaveField("Detail", ContainSubstring(server.Listener.Addr().String())),
		))
	}, hopTimeout)

	It("call a refused dial to HTTPS_PROXY unreachable, naming the host", func(ctx SpecContext) {
		proxy := httptest.NewServer(answer(http.StatusOK, ""))
		proxy.Close()
		client := github.NewHTTP(viaProxy(proxy.URL), mustParse("https://ghes.example.invalid/api/v3"), "o/r", "lg-test-token", clock.Real{})

		Expect(getAttempt(ctx, client)).To(BeBlocked(failure.Unreachable,
			HaveField("Detail", ContainSubstring("ghes.example.invalid")),
		))
	}, hopTimeout)

	DescribeTable("call a proxy's refusal to CONNECT unreachable, naming the proxy and the host",
		func(ctx SpecContext, status int) {
			proxy := serve(answer(status, ""))
			client := github.NewHTTP(viaProxy(proxy.URL), mustParse("https://ghes.example.invalid/api/v3"), "o/r", "lg-test-token", clock.Real{})

			Expect(getAttempt(ctx, client)).To(BeBlocked(failure.Unreachable,
				HaveField("Detail", ContainSubstring(mustParse(proxy.URL).Host)),
				HaveField("Detail", ContainSubstring("ghes.example.invalid:443")),
				HaveField("Detail", ContainSubstring(strconv.Itoa(status))),
			))
		},
		Entry("502", http.StatusBadGateway, hopTimeout),
		Entry("503", http.StatusServiceUnavailable, hopTimeout),
		Entry("504", http.StatusGatewayTimeout, hopTimeout),
		Entry("403", http.StatusForbidden, hopTimeout),
		Entry("407", http.StatusProxyAuthRequired, hopTimeout),
	)
})

// viaProxy is a transport that sends every request through proxyURL.
func viaProxy(proxyURL string) http.RoundTripper {
	transport := github.NewTransport(shortTimeouts)
	github.TransportOf(transport).Proxy = http.ProxyURL(mustParse(proxyURL))
	return transport
}

var _ = Describe("HTTP rate-limit reserve", Label("blocked"), func() {
	var reset time.Time

	BeforeEach(func() {
		reset = start.Add(17 * time.Minute)
	})

	// setRemaining sets the rate-limit headers of a server whose clock reads start minus behind.
	setRemaining := func(header http.Header, remaining string, behind time.Duration) {
		header.Set("Date", start.Add(-behind).Format(http.TimeFormat))
		header.Set("X-RateLimit-Limit", "100")
		header.Set("X-RateLimit-Remaining", remaining)
		header.Set("X-RateLimit-Reset", strconv.FormatInt(reset.Add(-behind).Unix(), 10))
	}

	withRemaining := func(remaining string, behind time.Duration) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			setRemaining(w.Header(), remaining, behind)
			_, _ = w.Write([]byte(attemptBody))
		}
	}

	It("sends no request after a response says fewer than 10% of the limit remain, until the reset", func(ctx SpecContext) {
		server, served := counting(withRemaining("9", 0))
		clk := clock.NewFake(start)
		client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clk)

		Expect(getAttempt(ctx, client)).To(Succeed())
		Expect(getAttempt(ctx, client)).To(Equal(failure.Blocked{
			Kind:    failure.RateLimit,
			Detail:  "X-RateLimit-Remaining 9 is below 10% of X-RateLimit-Limit 100",
			RetryAt: reset,
		}))
		Expect(served.Load()).To(BeEquivalentTo(1))

		clk.Set(reset)
		Expect(getAttempt(ctx, client)).To(Succeed())
		Expect(served.Load()).To(BeEquivalentTo(2))
	}, hopTimeout)

	It("measures the reset from the response's Date", func(ctx SpecContext) {
		server, served := counting(withRemaining("9", 2*time.Hour))
		clk := clock.NewFake(start)
		client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clk)

		Expect(getAttempt(ctx, client)).To(Succeed())
		clk.Set(start.Add(time.Minute))
		Expect(getAttempt(ctx, client)).To(BeBlocked(failure.RateLimit, HaveField("RetryAt", reset)))
		Expect(served.Load()).To(BeEquivalentTo(1))

		clk.Set(reset)
		Expect(getAttempt(ctx, client)).To(Succeed())
		Expect(served.Load()).To(BeEquivalentTo(2))
	}, hopTimeout)

	It("counts only API responses, not a redirected log's blob response", func(ctx SpecContext) {
		blob := serve(answer(http.StatusOK, "log"))
		api, served := counting(func(w http.ResponseWriter, r *http.Request) {
			setRemaining(w.Header(), "9", 0)
			http.Redirect(w, r, blob.URL+"/log", http.StatusFound)
		})
		client := github.NewHTTP(http.DefaultTransport, mustParse(api.URL), "o/r", "lg-test-token", clock.NewFake(start))

		Expect(client.DownloadJobLog(ctx, 1, &bytes.Buffer{})).To(Succeed())
		Expect(getAttempt(ctx, client)).To(BeBlocked(failure.RateLimit, HaveField("RetryAt", reset)))
		Expect(served.Load()).To(BeEquivalentTo(1))
	}, hopTimeout)

	It("keeps sending while 10% remain", func(ctx SpecContext) {
		server, served := counting(withRemaining("10", 0))
		client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.NewFake(start))

		for range 3 {
			Expect(getAttempt(ctx, client)).To(Succeed())
		}
		Expect(served.Load()).To(BeEquivalentTo(3))
	}, hopTimeout)
})

var _ = Describe("GetRepo", Label("blocked"), func() {
	It("returns the repo's full name as GitHub spells it", func(ctx SpecContext) {
		var path string
		server := serve(func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			_, _ = w.Write([]byte(`{"id":1402714635,"full_name":"rosenhouse/Lg"}`))
		})
		client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "rosenhouse/lg", "lg-test-token", clock.Real{})

		Expect(client.GetRepo(ctx)).To(HaveField("FullName", "rosenhouse/Lg"))
		Expect(path).To(Equal("/repos/rosenhouse/lg"))
	}, hopTimeout)

	It("returns ErrNotFound on a 404", func(ctx SpecContext) {
		server := serve(answer(http.StatusNotFound, gitHubNotFound))
		client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{})

		_, err := client.GetRepo(ctx)
		Expect(err).To(MatchError(github.ErrNotFound))
	}, hopTimeout)

	It("calls a full_name that is not owner/name malformed, naming it", func(ctx SpecContext) {
		server := serve(answer(http.StatusOK, `{"full_name":"../../escaped"}`))
		client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{})

		_, err := client.GetRepo(ctx)
		var malformed *github.MalformedError
		Expect(errors.As(err, &malformed)).To(BeTrue(), "%v", err)
		Expect(err).To(MatchError(server.URL + `/repos/o/r: full_name "../../escaped" is not owner/name`))
	}, hopTimeout)

	It("calls a body with no full_name malformed", func(ctx SpecContext) {
		server := serve(answer(http.StatusOK, `{"id":1}`))
		client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{})

		_, err := client.GetRepo(ctx)
		var malformed *github.MalformedError
		Expect(errors.As(err, &malformed)).To(BeTrue(), "%v", err)
		Expect(err).To(MatchError(server.URL + `/repos/o/r: full_name "" is not owner/name`))
	}, hopTimeout)
})
