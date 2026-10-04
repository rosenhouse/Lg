package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
)

var _ = Describe("the fakegithub dev server", Label("transport"), func() {
	start := func(args ...string) *gexec.Session {
		GinkgoHelper()
		session, err := gexec.Start(exec.CommandContext(GinkgoT().Context(), serverPath, args...), GinkgoWriter, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(session.Kill)
		return session
	}

	fetch := func(url string) (int, http.Header, string) {
		GinkgoHelper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		return resp.StatusCode, resp.Header, string(body)
	}

	get := func(url string) (http.Header, []byte) {
		GinkgoHelper()
		status, header, body := fetch(url)
		Expect(status).To(Equal(http.StatusOK), url)
		return header, []byte(body)
	}

	It("serves each -run at its stage, paging at -page-cap", func() {
		session := start("-run", "37129390741=after-attempt-1", "-run", "37129738159=logs-deleted", "-page-cap", "5", "-addr", "127.0.0.1:0")
		Eventually(session.Out, "5s").Should(gbytes.Say(`serving http://127\.0\.0\.1:\d+\n`))
		url := regexp.MustCompile(`http://\S+`).FindString(string(session.Out.Contents()))

		_, body := get(url + "/repos/rosenhouse/lg/actions/runs?per_page=100")
		var runs struct {
			WorkflowRuns []struct{ ID int64 } `json:"workflow_runs"`
		}
		Expect(json.Unmarshal(body, &runs)).To(Succeed())
		Expect(runs.WorkflowRuns).To(HaveExactElements(HaveField("ID", int64(37129738159)), HaveField("ID", int64(37129390741))))

		header, body := get(url + "/repos/rosenhouse/lg/actions/runs/37129390741/attempts/1/jobs?per_page=100")
		var jobs struct{ Jobs []any }
		Expect(json.Unmarshal(body, &jobs)).To(Succeed())
		Expect(jobs.Jobs).To(HaveLen(5))
		Expect(header.Get("Link")).To(ContainSubstring(`rel="next"`))
	})

	It("injects each -fail fault", Label("failures"), func() {
		session := start("-run", "37129390741=after-attempt-1", "-addr", "127.0.0.1:0",
			"-fail", "api,jobs/111221289861/logs,502,1",
			"-fail", "api,jobs/111221289875/logs,drop",
			"-fail", "api,jobs/111221289888/logs,truncate",
			"-fail", "api,jobs/111221289909/logs,stall",
			"-fail", "api,jobs/111221289911/logs,truncate+stall")
		Eventually(session.Out, "5s").Should(gbytes.Say(`serving http://127\.0\.0\.1:\d+\n`))
		logs := regexp.MustCompile(`http://\S+`).FindString(string(session.Out.Contents())) + "/repos/rosenhouse/lg/actions/jobs/"

		// try GETs url without following a redirect, giving up after a second.
		try := func(url string) (status int, headerErr, bodyErr error) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
			Expect(err).NotTo(HaveOccurred())
			client := &http.Client{Transport: &http.Transport{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := client.Do(req)
			if err != nil {
				return 0, err, nil
			}
			defer func() { _ = resp.Body.Close() }()
			_, err = io.ReadAll(resp.Body)
			return resp.StatusCode, nil, err
		}

		Expect(try(logs + "111221289861/logs")).To(Equal(http.StatusBadGateway))
		Expect(try(logs + "111221289861/logs")).To(Equal(http.StatusFound))
		_, err, _ := try(logs + "111221289875/logs")
		Expect(err).To(MatchError(ContainSubstring(`malformed HTTP status code "2"`)))
		_, _, err = try(logs + "111221289888/logs")
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
		_, err, _ = try(logs + "111221289909/logs")
		Expect(err).To(MatchError(context.DeadlineExceeded))
		_, _, err = try(logs + "111221289911/logs")
		Expect(err).To(MatchError(context.DeadlineExceeded))
	})

	It("answers a body=FILE -fail with a 200 of FILE's bytes", Label("artifacts"), func() {
		file := filepath.Join(GinkgoT().TempDir(), "bad.zip")
		Expect(os.WriteFile(file, []byte("not a zip"), 0o600)).To(Succeed())
		session := start("-run", "37129390741=after-attempt-1", "-addr", "127.0.0.1:0", "-fail", "blob,/artifacts/11276401837.zip,body="+file)
		Eventually(session.Out, "5s").Should(gbytes.Say(`serving http://127\.0\.0\.1:\d+\n`))
		url := regexp.MustCompile(`http://\S+`).FindString(string(session.Out.Contents()))

		_, body := get(url + "/repos/rosenhouse/lg/actions/artifacts/11276401837/zip")
		Expect(string(body)).To(Equal("not a zip"))
	})

	It("lists each -expire artifact as expired", Label("artifacts"), func() {
		session := start("-run", "37129390741=after-attempt-1", "-addr", "127.0.0.1:0", "-expire", "11275917910")
		Eventually(session.Out, "5s").Should(gbytes.Say(`serving http://127\.0\.0\.1:\d+\n`))
		url := regexp.MustCompile(`http://\S+`).FindString(string(session.Out.Contents()))

		_, body := get(url + "/repos/rosenhouse/lg/actions/runs/37129390741/artifacts?per_page=100")
		var listing struct {
			Artifacts []struct {
				ID      int64
				Expired bool
			}
		}
		Expect(json.Unmarshal(body, &listing)).To(Succeed())
		Expect(listing.Artifacts).To(HaveLen(4))
		for _, artifact := range listing.Artifacts {
			Expect(artifact.Expired).To(Equal(artifact.ID == 11275917910), "artifact %d", artifact.ID)
		}
	})

	// fetch GETs url, whatever its status.
	It("sets the rate limit from -rate-limit and the clock from -now", Label("blocked"), func() {
		now := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
		session := start("-run", "37129390741=after-attempt-1", "-addr", "127.0.0.1:0", "-rate-limit", "100,12", "-now", now.Format(time.RFC3339))
		Eventually(session.Out, "5s").Should(gbytes.Say(`serving http://127\.0\.0\.1:\d+\n`))
		url := regexp.MustCompile(`http://\S+`).FindString(string(session.Out.Contents()))

		header, _ := get(url + "/repos/rosenhouse/lg")
		Expect(header).To(SatisfyAll(
			HaveKeyWithValue("X-Ratelimit-Limit", []string{"100"}),
			HaveKeyWithValue("X-Ratelimit-Remaining", []string{"11"}),
		))
		Expect(clockOf(header)).To(BeTemporally("~", now, 5*time.Second))
		Eventually(func() time.Time {
			header, _ := get(url + "/repos/rosenhouse/lg")
			return clockOf(header)
		}, "5s").Should(BeTemporally(">", clockOf(header)))
	})

	It("takes its clock from the real time without -now", Label("blocked"), func() {
		session := start("-run", "37129390741=after-attempt-1", "-addr", "127.0.0.1:0")
		Eventually(session.Out, "5s").Should(gbytes.Say(`serving http://127\.0\.0\.1:\d+\n`))
		url := regexp.MustCompile(`http://\S+`).FindString(string(session.Out.Contents()))

		header, _ := get(url + "/repos/rosenhouse/lg")
		Expect(clockOf(header)).To(BeTemporally("~", clock.Real{}.Now(), 5*time.Second))
	})

	It("injects each rate-limit -fail kind", Label("blocked"), func() {
		session := start("-run", "37129390741=after-attempt-1", "-addr", "127.0.0.1:0",
			"-fail", "api,/repos/rosenhouse/lg,ratelimit,1",
			"-fail", "api,/repos/rosenhouse/lg,secondary,1",
			"-fail", "api,/repos/rosenhouse/lg,retry-after=30,1")
		Eventually(session.Out, "5s").Should(gbytes.Say(`serving http://127\.0\.0\.1:\d+\n`))
		repo := regexp.MustCompile(`http://\S+`).FindString(string(session.Out.Contents())) + "/repos/rosenhouse/lg"

		status, header, _ := fetch(repo)
		Expect(status).To(Equal(http.StatusForbidden))
		Expect(header.Get("X-RateLimit-Remaining")).To(Equal("0"))
		status, header, body := fetch(repo)
		Expect(status).To(Equal(http.StatusForbidden))
		Expect(header.Get("X-RateLimit-Remaining")).NotTo(Equal("0"))
		Expect(body).To(ContainSubstring("You have exceeded a secondary rate limit"))
		status, header, _ = fetch(repo)
		Expect(status).To(Equal(http.StatusTooManyRequests))
		Expect(header.Get("Retry-After")).To(Equal("30"))
	})

	DescribeTable("exits 2 naming a bad -rate-limit or -now",
		func(flag, value, message string) {
			session := start("-run", "37129390741=after-attempt-1", flag, value, "-addr", "127.0.0.1:0")
			Eventually(session, "5s").Should(gexec.Exit(2))
			Expect(session.Err).To(gbytes.Say(regexp.QuoteMeta(message)))
		},
		Entry("one field", "-rate-limit", "100", `-rate-limit "100": want LIMIT,REMAINING with 0 <= REMAINING <= LIMIT`, Label("blocked")),
		Entry("more remaining than the limit", "-rate-limit", "100,101", `-rate-limit "100,101": want LIMIT,REMAINING with 0 <= REMAINING <= LIMIT`, Label("blocked")),
		Entry("a negative remaining", "-rate-limit", "100,-1", `-rate-limit "100,-1": want LIMIT,REMAINING with 0 <= REMAINING <= LIMIT`, Label("blocked")),
		Entry("a -now that is not RFC 3339", "-now", "yesterday", `-now "yesterday": want an RFC 3339 time`, Label("blocked")),
	)

	DescribeTable("exits 2 naming a -fail that is not HOST,MATCH,KIND[,TIMES]",
		func(fail string) {
			session := start("-run", "37129390741=after-attempt-1", "-fail", fail, "-addr", "127.0.0.1:0")
			Eventually(session, "5s").Should(gexec.Exit(2))
			Expect(session.Err).To(gbytes.Say(regexp.QuoteMeta(`-fail "` + fail + `": want HOST,MATCH,KIND[,TIMES] with HOST api or blob and KIND a status, drop, truncate, stall, truncate+stall, ratelimit, secondary, retry-after=SECONDS or body=FILE`)))
		},
		Entry("too few fields", "api,jobs/1/logs", Label("failures")),
		Entry("too many fields", "api,jobs/1/logs,502,1,2", Label("failures")),
		Entry("an unknown host", "web,jobs/1/logs,502", Label("failures")),
		Entry("an unknown kind", "api,jobs/1/logs,slow", Label("failures")),
		Entry("a status that is not one", "api,jobs/1/logs,99", Label("failures")),
		Entry("TIMES that is not a count", "api,jobs/1/logs,502,-1", Label("failures")),
		Entry("a Retry-After that is not a count", "api,jobs/1/logs,retry-after=-1", Label("blocked")),
	)

	It("exits 2 naming a body=FILE -fail whose FILE it cannot read", Label("artifacts"), func() {
		session := start("-run", "37129390741=after-attempt-1", "-fail", "blob,/artifacts/1.zip,body=/no/such/file", "-addr", "127.0.0.1:0")
		Eventually(session, "5s").Should(gexec.Exit(2))
		Expect(session.Err).To(gbytes.Say(`-fail "blob,/artifacts/1.zip,body=/no/such/file": open /no/such/file: no such file or directory`))
	})

	It("exits 2 naming an -expire that is not an artifact id", Label("artifacts"), func() {
		session := start("-run", "37129390741=after-attempt-1", "-expire", "flaky-report", "-addr", "127.0.0.1:0")
		Eventually(session, "5s").Should(gexec.Exit(2))
		Expect(session.Err).To(gbytes.Say(`-expire "flaky-report": want an artifact ID`))
	})

	It("exits 1 naming an -addr already in use", func() {
		taken, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(taken.Close)

		session := start("-run", "37129390741=after-attempt-1", "-addr", taken.Addr().String())
		Eventually(session, "5s").Should(gexec.Exit(1))
		Expect(session.Err).To(gbytes.Say(regexp.QuoteMeta(taken.Addr().String()) + ".*address already in use"))
	})

	It("exits 2 naming a -run that is not ID=STAGE", func() {
		session := start("-run", "37129390741")
		Eventually(session, "5s").Should(gexec.Exit(2))
		Expect(session.Err).To(gbytes.Say(`-run "37129390741": want ID=STAGE`))
	})

	It("exits 2 naming a -run ID given twice", func() {
		session := start("-run", "37129390741=after-attempt-1", "-run", "37129390741=after-attempt-3", "-addr", "127.0.0.1:0")
		Eventually(session, "5s").Should(gexec.Exit(2))
		Expect(session.Err).To(gbytes.Say(`-run 37129390741 given twice`))
	})

	It("exits 2 for a negative -page-cap", func() {
		session := start("-run", "37129390741=after-attempt-1", "-page-cap", "-5", "-addr", "127.0.0.1:0")
		Eventually(session, "5s").Should(gexec.Exit(2))
		Expect(session.Err).To(gbytes.Say(`-page-cap must be 0 or more`))
	})

	It("exits 2 naming an argument that is not a flag", func() {
		session := start("-run", "37129390741=after-attempt-1", "37129738159=logs-deleted", "-addr", "127.0.0.1:0")
		Eventually(session, "5s").Should(gexec.Exit(2))
		Expect(session.Err).To(gbytes.Say(`unexpected argument "37129738159=logs-deleted"`))
	})

	It("exits 1 naming a recording that does not exist", func() {
		session := start("-run", "37129390741=no-such-stage", "-addr", "127.0.0.1:0")
		Eventually(session, "5s").Should(gexec.Exit(1))
		Expect(session.Err).To(gbytes.Say(`no recording of run 37129390741`))
	})
})

// clockOf gives the server's time from header's Date, checking that
// X-RateLimit-Reset is an hour later.
func clockOf(header http.Header) time.Time {
	GinkgoHelper()
	date, err := http.ParseTime(header.Get("Date"))
	Expect(err).NotTo(HaveOccurred())
	reset, err := strconv.ParseInt(header.Get("X-Ratelimit-Reset"), 10, 64)
	Expect(err).NotTo(HaveOccurred())
	Expect(time.Unix(reset, 0)).To(BeTemporally("~", date.Add(fakegithub.ResetAfter), time.Second))
	return date
}
