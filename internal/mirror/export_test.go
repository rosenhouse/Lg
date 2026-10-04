package mirror

import "github.com/rosenhouse/lg/internal/github"

var RunScoped = runScoped

func (m *Mirror) RetrySet(runDir string, listing, pending []github.Artifact) ([]github.Artifact, error) {
	return m.retrySet(listedRun{dir: runDir, artifacts: listing}, pending)
}
