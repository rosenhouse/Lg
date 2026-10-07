package mirror_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/model"
)

var _ = Describe("MatchingPRs", Label("prs"), func() {
	const (
		repoID = 100
		forkID = 200
	)
	var (
		fromRepo    = attemptRepos(repoID, repoID)
		fromFork    = attemptRepos(repoID, forkID)
		deletedFork = attemptRepos(repoID, 0)
	)

	DescribeTable("for a run's head branch and repository",
		func(run model.RunRepositories, branch string, pulls []github.CommitPull, want []int) {
			Expect(mirror.MatchingPRs(run, branch, pulls)).To(Equal(want))
		},
		Entry("keeps every PR whose head matches, sorted and deduplicated, and drops the others", fromRepo, "b", []github.CommitPull{
			{Number: 9, HeadRef: "b", HeadRepoID: repoID},
			{Number: 3, HeadRef: "main", HeadRepoID: repoID},
			{Number: 4, HeadRef: "b", HeadRepoID: repoID},
			{Number: 9, HeadRef: "b", HeadRepoID: repoID},
		}, []int{4, 9}),
		Entry("gives [] when none matches", fromRepo, "main", []github.CommitPull{{Number: 3, HeadRef: "b", HeadRepoID: repoID}}, []int{}),
		Entry("drops a PR whose head ref matches but whose head repo differs, for a run from the repository", fromRepo, "main", []github.CommitPull{{Number: 3, HeadRef: "main", HeadRepoID: forkID}}, []int{}),
		Entry("drops a PR whose head ref matches but whose head repo differs, for a fork run", fromFork, "main", []github.CommitPull{{Number: 3, HeadRef: "main", HeadRepoID: repoID}}, []int{}),
		Entry("keeps a PR from the fork's head repo for a fork run", fromFork, "main", []github.CommitPull{{Number: 3, HeadRef: "main", HeadRepoID: forkID}}, []int{3}),
		Entry("keeps a PR with a null head repo on head ref alone for a fork run", fromFork, "main", []github.CommitPull{{Number: 3, HeadRef: "main"}}, []int{3}),
		Entry("keeps a PR on head ref alone for a run from a deleted fork", deletedFork, "main", []github.CommitPull{{Number: 3, HeadRef: "main", HeadRepoID: repoID}}, []int{3}),
		Entry("never keeps a PR with a null head repo for a run from the repository", fromRepo, "main", []github.CommitPull{{Number: 3, HeadRef: "main"}}, []int{}),
	)
})

// attemptRepos gives a run's repository and head repository, with a head
// repo id of 0 as null.
func attemptRepos(repoID, headRepoID int64) model.RunRepositories {
	r := model.RunRepositories{}
	r.Repository.ID = repoID
	if headRepoID != 0 {
		r.HeadRepository = &struct{ ID int64 }{headRepoID}
	}
	return r
}
