package fakegithub_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("GET /commits/{sha}/pulls", Label("prs"), func() {
	var (
		fake       *fakegithub.Server
		shared     = strings.Repeat("a", 40)
		unlisted   = strings.Repeat("c", 40)
		fromBranch = scenario.CommitPull{Number: 1, HeadRef: "fix", HeadRepoID: scenario.RepoID}
		fromFork   = scenario.CommitPull{Number: 2, HeadRef: "main"}
		other      = scenario.CommitPull{Number: 3, HeadRef: "other", HeadRepoID: 9}
	)
	// consistOf matches the pull requests that WithCommitPulls lists for prs, in any order.
	consistOf := func(prs ...scenario.CommitPull) types.GomegaMatcher {
		var pulls []json.RawMessage
		Expect(json.Unmarshal(scenario.WithCommitPulls(scenario.Run{Files: fstest.MapFS{}}, prs...).Files[scenario.CommitPullsFile].Data, &pulls)).To(Succeed())
		var matchers []any
		for _, pull := range pulls {
			matchers = append(matchers, MatchJSON(pull))
		}
		return ConsistOf(matchers...)
	}
	run := func(id int64, sha string, prs ...scenario.CommitPull) scenario.Run {
		r := scenario.WithSHA(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), id), sha)
		if prs != nil {
			r = scenario.WithCommitPulls(r, prs...)
		}
		return r
	}

	BeforeEach(func() {
		fake = fakegithub.New()
		DeferCleanup(fake.Close)
		for _, r := range []scenario.Run{run(7, shared, fromBranch, fromFork), run(8, shared, fromFork, other), run(9, unlisted)} {
			Expect(fake.AddRun(r)).To(Succeed())
		}
	})

	It("serves the union of the WithCommitPulls lists of the loaded runs with that head_sha, [] when none set one, and 422 when none has it", func() {
		for _, repo := range []string{"/repos/rosenhouse/lg", "/api/v3/repos/rosenhouse/lg", "/repositories/" + repoID} {
			listed := fetch(fake.URL() + repo + "/commits/" + shared + "/pulls")
			Expect(listed.status).To(Equal(http.StatusOK), repo)
			var pulls []json.RawMessage
			Expect(json.Unmarshal(listed.body, &pulls)).To(Succeed())
			Expect(pulls).To(consistOf(fromBranch, fromFork, other))

			Expect(fetch(fake.URL() + repo + "/commits/" + unlisted + "/pulls").body).To(MatchJSON(`[]`))
			Expect(fetch(fake.URL() + repo + "/commits/" + strings.Repeat("f", 40) + "/pulls").status).To(Equal(http.StatusUnprocessableEntity))
		}
	})

	It("pages the bare array with Link rel=next URLs in /repositories/1402714635/ form when a page cap is set", func() {
		fake.SetPageCap(1)

		var pulls []json.RawMessage
		var followed []string
		for next := fake.URL() + "/repos/rosenhouse/lg/commits/" + shared + "/pulls?per_page=100"; next != ""; {
			page := fetch(next)
			Expect(page.status).To(Equal(http.StatusOK))
			var elements []json.RawMessage
			Expect(json.Unmarshal(page.body, &elements)).To(Succeed())
			pulls = append(pulls, elements...)
			next = nextLink(page.header.Get("Link"))
			if next != "" {
				followed = append(followed, next)
			}
		}
		Expect(pulls).To(consistOf(fromBranch, fromFork, other))
		Expect(followed).To(HaveLen(2))
		for _, next := range followed {
			u, err := url.Parse(next)
			Expect(err).NotTo(HaveOccurred())
			Expect(u.Path).To(Equal("/repositories/" + repoID + "/commits/" + shared + "/pulls"))
		}
	})
})
