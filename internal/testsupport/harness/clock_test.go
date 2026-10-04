package harness_test

import (
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var later = harness.DefaultNow().AddDate(1, 0, 0)

func dateFrom(fake *fakegithub.Server) string {
	GinkgoHelper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fake.URL()+"/repos/rosenhouse/lg", http.NoBody)
	Expect(err).NotTo(HaveOccurred())
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.Body.Close()).To(Succeed())
	return resp.Header.Get("Date")
}

var _ = Describe("InProcess", Label("blocked"), func() {
	It("gives its fake the env's clock", func() {
		env := harness.InProcess()
		env.Clock.Set(later)

		Expect(dateFrom(env.Fake)).To(Equal(later.Format(http.TimeFormat)))
	})
})

var _ = Describe("Env.SetNow", Label("blocked"), func() {
	It("sets LG_TEST_NOW and the fake's clock together", func() {
		env := harness.New("/no/lg")
		fake := fakegithub.Start(37129390741, "after-attempt-1")

		env.SetNow(later, fake)

		session := env.Sh(`echo "$LG_TEST_NOW"`)
		Eventually(session, harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(string(session.Out.Contents())).To(Equal(later.Format(time.RFC3339) + "\n"))
		Expect(dateFrom(fake)).To(Equal(later.Format(http.TimeFormat)))
	})
})
