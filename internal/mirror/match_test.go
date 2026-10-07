package mirror_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/model"
)

const (
	repoID = 100
	forkID = 200
)

// repos reads a run's repository and head_repository as attempt.json gives them.
func repos(attempt string) model.RunRepositories {
	GinkgoHelper()
	var r model.RunRepositories
	Expect(json.Unmarshal([]byte(attempt), &r)).To(Succeed())
	return r
}

var (
	fromRepo    = `{"repository": {"id": 100}, "head_repository": {"id": 100}}`
	fromFork    = `{"repository": {"id": 100}, "head_repository": {"id": 200}}`
	deletedFork = `{"repository": {"id": 100}, "head_repository": null}`
)

var _ = DescribeTable("MatchingPRs", Label("prs"),
	func(run, branch string, pulls []github.CommitPull, want []int) {
		Expect(mirror.MatchingPRs(repos(run), branch, pulls)).To(Equal(want))
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
