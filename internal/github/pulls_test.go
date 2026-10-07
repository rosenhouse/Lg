package github_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("CommitPulls", Label("prs"), func() {
	const sha = "1a51097dadb5b55978ac401b93f1ca9d8d317b02"

	It("lists the commit's pull requests on every page, with a null head repo as 0, and the listing's URL and page count", func() {
		fake := fakegithub.New()
		DeferCleanup(fake.Close)
		prs := []scenario.CommitPull{{Number: 58, HeadRef: "s22-skill", HeadRepoID: scenario.RepoID}, {Number: 7, HeadRef: "main"}}
		Expect(fake.AddRun(scenario.WithCommitPulls(scenario.Recorded(37129390741, "after-attempt-1"), prs...))).To(Succeed())
		fake.SetPageCap(1)
		client := github.NewHTTP(http.DefaultTransport, mustParse(fake.URL()), "rosenhouse/lg", "lg-test-token", clock.Real{})

		pulls, source, err := client.CommitPulls(context.Background(), sha)

		Expect(err).NotTo(HaveOccurred())
		Expect(pulls).To(ConsistOf(
			github.CommitPull{Number: 58, HeadRef: "s22-skill", HeadRepoID: scenario.RepoID},
			github.CommitPull{Number: 7, HeadRef: "main"},
		))
		Expect(source).To(Equal(github.Source{URL: fake.URL() + "/repos/rosenhouse/lg/commits/" + sha + "/pulls?per_page=100", Pages: 2}))
	})

	Describe("a 422", func() {
		var (
			fake   *fakegithub.Server
			client github.Client
		)

		BeforeEach(func() {
			fake = fakegithub.New()
			DeferCleanup(fake.Close)
			prs := []scenario.CommitPull{{Number: 1, HeadRef: "b", HeadRepoID: scenario.RepoID}, {Number: 2, HeadRef: "b", HeadRepoID: scenario.RepoID}}
			Expect(fake.AddRun(scenario.WithCommitPulls(scenario.Recorded(37129390741, "after-attempt-1"), prs...))).To(Succeed())
			fake.SetPageCap(1)
			client = github.NewHTTP(http.DefaultTransport, mustParse(fake.URL()), "rosenhouse/lg", "lg-test-token", clock.Real{})
		})

		It("on the first page gives ErrUnknownCommit with the listing's URL", func() {
			fake.Fail("api", "/repos/rosenhouse/lg/commits/"+sha+"/pulls", fakegithub.Fault{Status: http.StatusUnprocessableEntity})

			_, source, err := client.CommitPulls(context.Background(), sha)

			Expect(err).To(MatchError(github.ErrUnknownCommit))
			Expect(source).To(Equal(github.Source{URL: fake.URL() + "/repos/rosenhouse/lg/commits/" + sha + "/pulls?per_page=100", Pages: 1}))
		})

		It("on a later page is a plain status error", func() {
			fake.Fail("api", "/repositories/1402714635/commits/"+sha+"/pulls", fakegithub.Fault{Status: http.StatusUnprocessableEntity})

			_, _, err := client.CommitPulls(context.Background(), sha)

			Expect(err).To(MatchError(ContainSubstring("422")))
			Expect(err).NotTo(MatchError(github.ErrUnknownCommit))
		})
	})

	It("refuses a Link next that is not on the API host", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Link", `<http://blob.example/pulls?page=2>; rel="next"`)
			_, _ = w.Write([]byte(`[]`))
		}))
		DeferCleanup(server.Close)

		_, _, err := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{}).CommitPulls(context.Background(), sha)

		Expect(err).To(MatchError(ContainSubstring("is not on the API host")))
	})
})
