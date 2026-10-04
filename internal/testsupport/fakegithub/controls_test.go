package fakegithub_test

import (
	"context"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
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

	It("RequireToken answers 401 to API requests without Authorization: Bearer <token>", Label("transport"), func() {
		fake.RequireToken("lg-test-token")

		Expect(fetch(fake.URL() + logPath).status).To(Equal(http.StatusUnauthorized))
		Expect(fetch(fake.URL()+logPath, "Authorization", "Bearer other").status).To(Equal(http.StatusUnauthorized))
		Expect(fetch(fake.URL()+logPath, "Authorization", "token lg-test-token").status).To(Equal(http.StatusUnauthorized))
		Expect(fetch(fake.URL()+logPath, "Authorization", "Bearer lg-test-token").status).To(Equal(http.StatusFound))
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
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fake.URL()+logPath, http.NoBody)
			Expect(err).NotTo(HaveOccurred())
			resp, err := (&http.Client{Transport: &http.Transport{}}).Do(req)
			if err != nil {
				return nil, err
			}
			defer func() { _ = resp.Body.Close() }()
			return io.ReadAll(resp.Body)
		}
		recorded := func() []byte { return fake.Served("attempt-1/logs/111221289888.txt") }

		It("Drop closes the connection without a response", func(ctx SpecContext) {
			fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Drop: true})

			_, err := download(ctx)
			Expect(err).To(MatchError(io.EOF))
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

			body, err := download(ctx)
			Expect(err).To(MatchError(context.DeadlineExceeded))
			Expect(body).To(Equal(recorded()[:len(recorded())/2]))
		}, SpecTimeout(5*time.Second))
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
