package fakegithub_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

const (
	runID   = 37129390741
	logPath = "/repos/rosenhouse/lg/actions/jobs/111221289888/logs"
)

var _ = Describe("Server controls", func() {
	var fake *fakegithub.Server

	BeforeEach(func() {
		fake = fakegithub.Start(runID, "after-attempt-1")
	})

	served := func(url string) []byte {
		GinkgoHelper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp).To(HaveHTTPStatus(http.StatusOK), url)
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		return body
	}

	get := func(path string) int {
		GinkgoHelper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fake.URL()+path, http.NoBody)
		Expect(err).NotTo(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		_, err = io.Copy(io.Discard, resp.Body)
		Expect(err).NotTo(HaveOccurred())
		return resp.StatusCode
	}

	It("Remove deletes a run, so the run listing omits it and its routes and downloads 404", Label("artifacts"), func() {
		fake.Remove(runID)

		var listing struct {
			TotalCount int `json:"total_count"`
		}
		Expect(json.Unmarshal(served(fake.URL()+"/repos/rosenhouse/lg/actions/runs"), &listing)).To(Succeed())
		Expect(listing.TotalCount).To(BeZero())
		Expect(get("/repos/rosenhouse/lg/actions/runs/37129390741")).To(Equal(http.StatusNotFound))
		Expect(get("/repos/rosenhouse/lg/actions/runs/37129390741/artifacts")).To(Equal(http.StatusNotFound))
		Expect(get("/repos/rosenhouse/lg/actions/artifacts/11276401837/zip")).To(Equal(http.StatusNotFound))
	})

	It("Fail answers requests whose path ends in match with the fault's status, Times times", Label("store"), func() {
		fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 2})

		Expect(get(logPath)).To(Equal(http.StatusInternalServerError))
		Expect(get("/repos/rosenhouse/lg/actions/jobs/111221289875/logs")).To(Equal(http.StatusOK))
		Expect(get(logPath)).To(Equal(http.StatusInternalServerError))
		Expect(get(logPath)).To(Equal(http.StatusOK))
	})

	It("Fail on the blob host leaves the API's redirect alone", Label("store"), func() {
		fake.Fail("blob", "/logs/111221289888.txt", fakegithub.Fault{Status: http.StatusServiceUnavailable, Times: 1})

		Expect(get(logPath)).To(Equal(http.StatusServiceUnavailable))
		Expect(fake.Requests()).To(HaveExactElements(
			HaveField("Host", "api"),
			HaveField("Host", "blob"),
		))
	})

	It("Hold delays matching requests until released, recording them on arrival", Label("store"), func() {
		release := fake.Hold("/logs")
		done := make(chan int)
		go func() {
			defer GinkgoRecover()
			done <- get(logPath)
		}()

		Eventually(fake.Requests, time.Second).Should(ContainElement(HaveField("Path", logPath)))
		Consistently(done, 200*time.Millisecond).ShouldNot(Receive())
		Expect(get("/repos/rosenhouse/lg/actions/runs")).To(Equal(http.StatusOK))

		release()
		Eventually(done, time.Second).Should(Receive(Equal(http.StatusOK)))
		Expect(get(logPath)).To(Equal(http.StatusOK))
	})

	It("Close releases held requests", Label("store"), func() {
		// Runs before Start's Close, so a failing spec does not hang there.
		DeferCleanup(fake.Hold("/logs"))
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fake.URL()+logPath, http.NoBody)
			Expect(err).NotTo(HaveOccurred())
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}()
		Eventually(fake.Requests, time.Second).ShouldNot(BeEmpty())

		closed := make(chan struct{})
		go func() {
			fake.Close()
			close(closed)
		}()
		Eventually(closed, time.Second).Should(BeClosed())
		Eventually(done, time.Second).Should(BeClosed())
	})

	It("Close ends a stalled response", Label("failures"), func() {
		fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Stall: true})
		// Runs before Start's Close, so a failing spec does not hang there.
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		go func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fake.URL()+logPath, http.NoBody)
			if err != nil {
				return
			}
			if resp, err := (&http.Client{Transport: &http.Transport{}}).Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}()
		Eventually(fake.Requests, time.Second).ShouldNot(BeEmpty())

		closed := make(chan struct{})
		go func() {
			fake.Close()
			close(closed)
		}()
		Eventually(closed, time.Second).Should(BeClosed())
	})

	It("RequireToken answers 401 to API requests without Authorization: Bearer <token>", Label("transport"), func() {
		fake.RequireToken("lg-test-token")

		Expect(fetch(fake.URL() + logPath).status).To(Equal(http.StatusUnauthorized))
		Expect(fetch(fake.URL()+logPath, "Authorization", "Bearer other").status).To(Equal(http.StatusUnauthorized))
		Expect(fetch(fake.URL()+logPath, "Authorization", "token lg-test-token").status).To(Equal(http.StatusUnauthorized))
		Expect(fetch(fake.URL()+logPath, "Authorization", "Bearer lg-test-token").status).To(Equal(http.StatusFound))
	})

	It("AddRun serves a scenario run's listing, attempts, jobs and logs, and serves it again in place of the earlier one", Label("attempts"), func() {
		run := scenario.Clone(scenario.Recorded(runID, "after-attempt-2"), 7)
		Expect(fake.AddRun(scenario.InProgress(run, 2))).To(Succeed())
		Expect(fake.AddRun(run)).To(Succeed())

		Expect(served(fake.URL() + "/repos/rosenhouse/lg/actions/runs/7/attempts/2")).To(MatchJSON(run.Files["attempt-2/attempt.json"].Data))
		Expect(served(fake.URL() + "/repos/rosenhouse/lg/actions/runs/7/attempts/2/jobs")).To(MatchJSON(run.Files["attempt-2/jobs.json"].Data))
		Expect(served(fake.URL() + "/repos/rosenhouse/lg/actions/jobs/7111221661475/logs")).To(Equal(run.Files["attempt-2/logs/7111221661475.txt"].Data))
		var listing struct {
			WorkflowRuns []json.RawMessage `json:"workflow_runs"`
		}
		Expect(json.Unmarshal(served(fake.URL()+"/repos/rosenhouse/lg/actions/runs"), &listing)).To(Succeed())
		Expect(listing.WorkflowRuns).To(ContainElement(MatchJSON(run.Files["run.json"].Data)))
	})

	It("Advance refuses a run that is not loaded", Label("transport"), func() {
		Expect(fake.Advance(1, "after-attempt-3")).To(MatchError("run 1 is not loaded"))
		Expect(fetch(fake.URL() + "/repos/rosenhouse/lg/actions/runs/1").status).To(Equal(http.StatusNotFound))
	})

	It("Requests reports each request's status and whether it carried Authorization", Label("transport"), func() {
		fetch(fake.URL() + "/repos/rosenhouse/lg")
		fetch(fake.URL()+logPath, "Authorization", "Bearer lg-test-token")

		Expect(fake.Requests()).To(HaveExactElements(
			SatisfyAll(HaveField("Status", http.StatusOK), HaveField("Authorization", false)),
			SatisfyAll(HaveField("Status", http.StatusFound), HaveField("Authorization", true)),
		))
	})

	Describe("faults that break the connection", Label("failures"), func() {
		// download follows the redirect and reads the whole body, giving up after a second.
		download := func(ctx context.Context) ([]byte, error) {
			timeout, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(timeout, http.MethodGet, fake.URL()+logPath, http.NoBody)
			Expect(err).NotTo(HaveOccurred())
			resp, err := (&http.Client{Transport: &http.Transport{}}).Do(req)
			if err != nil {
				return nil, err
			}
			defer func() { _ = resp.Body.Close() }()
			return io.ReadAll(resp.Body)
		}
		recorded := func() []byte { return fake.Served("attempt-1/logs/111221289888.txt") }

		It("Drop closes the connection partway through the status line", func(ctx SpecContext) {
			fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Drop: true})

			_, err := download(ctx)
			Expect(err).To(MatchError(ContainSubstring(`malformed HTTP status code "2"`)))
		}, SpecTimeout(5*time.Second))

		It("Drop fails a request on a reused connection, which Go would otherwise retry", func(ctx SpecContext) {
			client := &http.Client{Transport: &http.Transport{}}
			get := func() error {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, fake.URL()+"/repos/rosenhouse/lg", http.NoBody)
				Expect(err).NotTo(HaveOccurred())
				resp, err := client.Do(req)
				if err != nil {
					return err
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				return resp.Body.Close()
			}
			Expect(get()).To(Succeed())
			fake.Fail("api", "/repos/rosenhouse/lg", fakegithub.Fault{Drop: true, Times: 1})

			Expect(get()).NotTo(Succeed())
		}, SpecTimeout(5*time.Second))

		It("Truncate declares the whole body's Content-Length and sends half of it", func(ctx SpecContext) {
			fake.Fail("blob", "/logs/111221289888.txt", fakegithub.Fault{Truncate: true})

			body, err := download(ctx)
			Expect(err).To(MatchError(io.ErrUnexpectedEOF))
			Expect(body).To(Equal(recorded()[:len(recorded())/2]))
		}, SpecTimeout(5*time.Second))

		It("Stall sends nothing until the client gives up", func(ctx SpecContext) {
			fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Stall: true})

			_, err := download(ctx)
			Expect(err).To(MatchError(context.DeadlineExceeded))
		}, SpecTimeout(5*time.Second))

		It("Truncate with Stall sends half the body, then nothing until the client gives up", func(ctx SpecContext) {
			fake.Fail("blob", "/logs/111221289888.txt", fakegithub.Fault{Truncate: true, Stall: true})
			timeout, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(timeout, http.MethodGet, fake.URL()+logPath, http.NoBody)
			Expect(err).NotTo(HaveOccurred())
			resp, err := (&http.Client{Transport: &http.Transport{}}).Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()

			half := make([]byte, len(recorded())/2)
			Expect(io.ReadFull(resp.Body, half)).To(Equal(len(half)))
			Expect(half).To(Equal(recorded()[:len(half)]))
			_, err = resp.Body.Read(make([]byte, 1))
			Expect(err).To(MatchError(context.DeadlineExceeded))
		}, SpecTimeout(5*time.Second))

		It("records a response's status once its headers are sent, before its body ends", func(ctx SpecContext) {
			fake.Fail("blob", "/logs/111221289888.txt", fakegithub.Fault{Truncate: true, Stall: true})
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fake.URL()+logPath, http.NoBody)
			Expect(err).NotTo(HaveOccurred())
			resp, err := (&http.Client{Transport: &http.Transport{}}).Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()

			Expect(fake.Requests()).To(HaveExactElements(
				HaveField("Status", http.StatusFound),
				HaveField("Status", http.StatusOK),
			))
		}, SpecTimeout(5*time.Second))
	})

	It("Fail with a Body answers with the fault's status and that body", Label("failures"), func() {
		fake.Fail("api", "runs/37129390741/attempts/1", fakegithub.Fault{Status: http.StatusOK, Body: "<html>unicorn</html>"})

		attempt := fetch(fake.URL() + "/repos/rosenhouse/lg/actions/runs/37129390741/attempts/1")
		Expect(attempt.status).To(Equal(http.StatusOK))
		Expect(string(attempt.body)).To(Equal("<html>unicorn</html>"))
	})

	DescribeTable("Fail on the blob host answers with blob storage's XML error", Label("failures"),
		func(status int, code string) {
			fake.Fail("blob", "/logs/111221289888.txt", fakegithub.Fault{Status: status})

			redirect := fetch(fake.URL() + logPath)
			blob := fetch(redirect.header.Get("Location"))
			Expect(blob.status).To(Equal(status))
			Expect(blob.header.Get("Content-Type")).To(Equal("application/xml"))
			Expect(string(blob.body)).To(ContainSubstring("<Code>" + code + "</Code>"))
		},
		Entry("404", http.StatusNotFound, "BlobNotFound"),
		Entry("403", http.StatusForbidden, "AuthenticationFailed"),
		Entry("503", http.StatusServiceUnavailable, "ServerBusy"),
	)
})
