package mirror

import (
	"errors"

	"github.com/rosenhouse/lg/internal/github"
)

var (
	RunScoped    = runScoped
	NonTerminal  = nonTerminal
	Merge        = merge
	RescanWindow = rescanWindow
	MatchingPRs  = matchingPRs
)

// Retry is an artifact of a retry set, with the run_attempt of its origin.
type Retry struct {
	github.Artifact
	RunAttempt int
}

// RetrySet gives the run's retry set.
func (m *Mirror) RetrySet(runDir string, listing, pending []github.Artifact) ([]Retry, error) {
	pendingCandidates := (&artifactListing{artifacts: pending}).candidates()
	onDisk, err := m.attemptsOnDisk(runDir)
	if err != nil {
		return nil, err
	}
	retry, unreadable, err := m.retrySet(runDir, onDisk, &artifactListing{artifacts: listing}, pendingCandidates)
	retries := make([]Retry, len(retry))
	for i, c := range retry {
		retries[i] = Retry{Artifact: c.Artifact, RunAttempt: c.Origin.RunAttempt}
	}
	return retries, errors.Join(unreadable, err)
}
