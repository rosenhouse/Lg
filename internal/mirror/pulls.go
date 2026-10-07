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
		found = lookUp(ctx, gh, attempt.HeadSHA)
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

// lookUp lists the commit's pull requests. GitHub answers 422 for a commit
// it does not know, which lists none.
func lookUp(ctx context.Context, gh github.Client, sha string) lookup {
	pulls, from, err := gh.CommitPulls(ctx, sha)
	var statusErr *github.StatusError
	if errors.As(err, &statusErr) && statusErr.Status == http.StatusUnprocessableEntity {
		return lookup{source: source{URL: statusErr.URL, Status: statusErr.Status}}
	}
	return lookup{pulls: pulls, source: source{URL: from.URL, Status: http.StatusOK, Pages: from.Pages}, err: err}
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
