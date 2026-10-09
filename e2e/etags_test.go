package e2e_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

var _ = Describe("a second lg sync against an unchanged repo", Label("etags"), func() {
	It("sends If-None-Match, gets 304s for all but the backfill listing, and leaves data/ unchanged", func() {
		env := harness.New(lgPath)
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		env.WriteConfig(fake.URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		first := len(fake.Requests())
		data := treesnap.Snapshot(env.Data())

		Expect(env.Sync()).To(gexec.Exit(0))
		second := fake.Requests()[first:]
		Expect(second).To(HaveLen(8), "the repo, the backfill listing and six listings by status")
		Expect(second).To(HaveEach(Or(
			HaveField("Status", http.StatusNotModified),
			HaveField("Query", ContainSubstring("created=")),
		)))
		Expect(second).To(ContainElement(HaveField("Query", ContainSubstring("created="))))
		Expect(treesnap.Snapshot(env.Data())).To(Equal(data))
	})
})
