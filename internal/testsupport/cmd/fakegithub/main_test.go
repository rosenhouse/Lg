package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

var _ = Describe("the fakegithub dev server", Label("transport"), func() {
	start := func(args ...string) *gexec.Session {
		GinkgoHelper()
		session, err := gexec.Start(exec.CommandContext(GinkgoT().Context(), serverPath, args...), GinkgoWriter, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(session.Kill)
		return session
	}

	get := func(url string) (http.Header, []byte) {
		GinkgoHelper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK), url)
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		return resp.Header, body
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
		Expect(err).To(MatchError(io.EOF))
		_, _, err = try(logs + "111221289888/logs")
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
		_, err, _ = try(logs + "111221289909/logs")
		Expect(err).To(MatchError(context.DeadlineExceeded))
		_, _, err = try(logs + "111221289911/logs")
		Expect(err).To(MatchError(context.DeadlineExceeded))
	})

	DescribeTable("exits 2 naming a -fail that is not HOST,MATCH,KIND[,TIMES]",
		func(fail string) {
			session := start("-run", "37129390741=after-attempt-1", "-fail", fail, "-addr", "127.0.0.1:0")
			Eventually(session, "5s").Should(gexec.Exit(2))
			Expect(session.Err).To(gbytes.Say(regexp.QuoteMeta(`-fail "`+fail+`": want HOST,MATCH,KIND[,TIMES] with HOST api or blob and KIND a status, drop, truncate, stall or truncate+stall`)))
		},
		Entry("too few fields", "api,jobs/1/logs", Label("failures")),
		Entry("too many fields", "api,jobs/1/logs,502,1,2", Label("failures")),
		Entry("an unknown host", "web,jobs/1/logs,502", Label("failures")),
		Entry("an unknown kind", "api,jobs/1/logs,slow", Label("failures")),
		Entry("a status that is not one", "api,jobs/1/logs,99", Label("failures")),
		Entry("TIMES that is not a count", "api,jobs/1/logs,502,-1", Label("failures")),
	)

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
