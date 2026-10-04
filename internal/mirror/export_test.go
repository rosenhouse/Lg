package mirror

import (
	"errors"

	"github.com/rosenhouse/lg/internal/github"
)

var RunScoped = runScoped

// RetrySet gives the artifacts of the run's retry set.
func (m *Mirror) RetrySet(runDir string, listing, pending []github.Artifact) ([]github.Artifact, error) {
	pendingCandidates := (&artifactListing{artifacts: pending}).candidates()
	retry, unreadable, err := m.retrySet(listedRun{dir: runDir, artifacts: &artifactListing{artifacts: listing}}, pendingCandidates)
	artifacts := make([]github.Artifact, len(retry))
	for i, c := range retry {
		artifacts[i] = c.Artifact
	}
	return artifacts, errors.Join(unreadable, err)
}
