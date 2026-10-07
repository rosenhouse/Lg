package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/model"
)

// lookups holds each head SHA's pull requests, or the error that looking
// them up gave, for the rest of a cycle.
type lookups map[string]lookup

type lookup struct {
	pulls  []github.CommitPull
	source source
	err    error
}

// commitPRs gives the attempt's commit_pr_numbers and their source. It looks
// each head SHA up once per cycle.
func (l lookups) commitPRs(ctx context.Context, gh github.Client, attempt github.Run) ([]int, source, error) {
	found, ok := l[attempt.HeadSHA]
	if !ok {
		found = listCommitPulls(ctx, gh, attempt.HeadSHA)
		l[attempt.HeadSHA] = found
	}
	if found.err != nil {
		return nil, source{}, found.err
	}
	var repos model.RunRepositories
	if err := json.Unmarshal(attempt.Raw, &repos); err != nil {
		return nil, source{}, &github.MalformedError{Err: err}
	}
	return matchingPRs(repos, attempt.HeadBranch, found.pulls), found.source, nil
}

// listCommitPulls lists the commit's pull requests. A commit GitHub does
// not know lists none.
func listCommitPulls(ctx context.Context, gh github.Client, sha string) lookup {
	pulls, from, err := gh.CommitPulls(ctx, sha)
	status := http.StatusOK
	switch {
	case errors.Is(err, github.ErrUnknownCommit):
		status = http.StatusUnprocessableEntity
	case err != nil:
		return lookup{err: err}
	}
	return lookup{pulls: pulls, source: source{URL: from.URL, Status: status, Pages: from.Pages}}
}

// matchingPRs gives the numbers of the pulls whose head is the run's head
// branch and repository, sorted. A null head repo, on either side, matches
// only a run from a fork.
func matchingPRs(run model.RunRepositories, headBranch string, pulls []github.CommitPull) []int {
	numbers := []int{}
	for _, pr := range pulls {
		if pr.HeadRef == headBranch && sameHeadRepo(run, pr.HeadRepoID) {
			numbers = append(numbers, pr.Number)
		}
	}
	slices.Sort(numbers)
	return slices.Compact(numbers)
}

func sameHeadRepo(run model.RunRepositories, prRepo int64) bool {
	if !run.FromFork() {
		return prRepo == run.Repository.ID
	}
	return prRepo == 0 || run.HeadRepository == nil || prRepo == run.HeadRepository.ID
}
