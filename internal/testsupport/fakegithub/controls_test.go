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

var _ = Describe("Server controls", Label("store"), func() {
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

	It("Fail answers requests whose path ends in match with the fault's status, Times times", func() {
		fake.Fail("api", "jobs/111221289888/logs", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 2})

		Expect(get(logPath)).To(Equal(http.StatusInternalServerError))
		Expect(get("/repos/rosenhouse/lg/actions/jobs/111221289875/logs")).To(Equal(http.StatusOK))
		Expect(get(logPath)).To(Equal(http.StatusInternalServerError))
		Expect(get(logPath)).To(Equal(http.StatusOK))
	})

	It("Fail on the blob host leaves the API's redirect alone", func() {
		fake.Fail("blob", "/logs/111221289888", fakegithub.Fault{Status: http.StatusServiceUnavailable, Times: 1})

		Expect(get(logPath)).To(Equal(http.StatusServiceUnavailable))
		Expect(fake.Requests()).To(HaveExactElements(
			HaveField("Host", "api"),
			HaveField("Host", "blob"),
		))
	})

	It("Hold delays matching requests until released, recording them on arrival", func() {
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

	It("Close releases held requests", func() {
		fake.Hold("/logs")
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

		fake.Close()
		Eventually(done, time.Second).Should(BeClosed())
	})
})
