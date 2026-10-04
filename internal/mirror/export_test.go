package mirror

import (
	"errors"

	"github.com/rosenhouse/lg/internal/github"
)

var RunScoped = runScoped

// Retry is an artifact of a retry set, with the run_attempt of its origin.
type Retry struct {
	github.Artifact
	RunAttempt int
}

// RetrySet gives the run's retry set.
func (m *Mirror) RetrySet(runDir string, listing, pending []github.Artifact) ([]Retry, error) {
	pendingCandidates := (&artifactListing{artifacts: pending}).candidates()
	retry, unreadable, err := m.retrySet(listedRun{dir: runDir, artifacts: &artifactListing{artifacts: listing}}, pendingCandidates)
	retries := make([]Retry, len(retry))
	for i, c := range retry {
		retries[i] = Retry{Artifact: c.Artifact, RunAttempt: c.Origin.RunAttempt}
	}
	return retries, errors.Join(unreadable, err)
}
