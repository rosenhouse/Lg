package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"

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

	It("serves each -run at its stage on -addr, paging at -page-cap", func() {
		free, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		addr := free.Addr().String()
		Expect(free.Close()).To(Succeed())
		url := "http://" + addr

		session := start("-run", "37129390741=after-attempt-1", "-run", "37129738159=logs-deleted", "-page-cap", "5", "-addr", addr)
		Eventually(session.Out, "5s").Should(gbytes.Say("serving " + regexp.QuoteMeta(url) + "\n"))

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

	It("exits 2 naming a -run that is not ID=STAGE", func() {
		session := start("-run", "37129390741")
		Eventually(session, "5s").Should(gexec.Exit(2))
		Expect(session.Err).To(gbytes.Say(`-run "37129390741": want ID=STAGE`))
	})

	It("exits 1 naming a recording that does not exist", func() {
		session := start("-run", "37129390741=no-such-stage", "-addr", "127.0.0.1:0")
		Eventually(session, "5s").Should(gexec.Exit(1))
		Expect(session.Err).To(gbytes.Say(`no recording of run 37129390741`))
	})
})
