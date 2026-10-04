package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/tombstone"
)

// syncArtifacts lists the run's artifacts into run and publishes its retry
// set. It returns the errors that runScoped accepts, and stops at any other.
// A run not found is skipped.
func (m *Mirror) syncArtifacts(ctx context.Context, gh github.Client, run *listedRun) ([]error, error) {
	artifacts, listing, err := gh.ListArtifacts(ctx, run.ID)
	if errors.Is(err, github.ErrNotFound) {
		return nil, nil
	}
	if runScoped(err) {
		return []error{fmt.Errorf("run %d artifacts: %w", run.ID, err)}, nil
	}
	if err != nil {
		return nil, err
	}
	run.artifactsListed, run.artifacts, run.listing = true, artifacts, listing
	retry, err := m.retrySet(*run, nil)
	if err != nil {
		return nil, err
	}
	var failed []error
	for _, artifact := range retry {
		err := m.publishArtifact(ctx, gh, run.Run, listing, artifact, layout.ArtifactDir(run.dir, artifact.ID, artifact.Name))
		switch {
		case runScoped(err):
			failed = append(failed, fmt.Errorf("run %d artifact %d: %w", run.ID, artifact.ID, err))
		case err != nil:
			return nil, err
		}
	}
	return failed, nil
}

func (m *Mirror) publishArtifact(ctx context.Context, gh github.Client, run github.Run, listing github.Source, artifact github.Artifact, target string) error {
	unit, err := m.Store.NewUnit()
	if err != nil {
		return err
	}
	s := &staged{unit: unit, sources: map[string]source{}}
	err = s.writeJSON("artifact.json", artifact.Raw, listing)
	if err == nil {
		err = m.stageZip(ctx, gh, s, artifact)
	}
	if err == nil {
		err = m.writeArtifactFetch(s, run)
	}
	if err == nil {
		err = m.Store.Publish(unit, target)
	}
	if err != nil {
		return errors.Join(err, unit.Abort())
	}
	return nil
}

// stageZip stages artifact.zip, or its tombstone when GitHub no longer has
// it or it is over ArtifactMaxBytes.
func (m *Mirror) stageZip(ctx context.Context, gh github.Client, s *staged, artifact github.Artifact) error {
	url := gh.ArtifactZipURL(artifact.ID)
	switch model.ClassifyArtifact(artifact.Artifact, m.ArtifactMaxBytes) {
	case model.ArtifactExpired:
		return writeTombstone(s.unit, ".", tombstone.New("artifact.zip", url, tombstone.Expired, "GitHub lists the artifact as expired", m.Clock.Now()))
	case model.ArtifactTooLarge:
		message := fmt.Sprintf("size_in_bytes %d exceeds artifact_max_bytes %d", artifact.SizeInBytes, m.ArtifactMaxBytes)
		return writeTombstone(s.unit, ".", tombstone.New("artifact.zip", url, tombstone.TooLarge, message, m.Clock.Now()))
	}
	want, err := artifact.SHA256()
	if err != nil {
		return &github.MalformedError{Err: err}
	}
	download := func(w io.Writer) error { return gh.DownloadArtifact(ctx, artifact.ID, w) }
	lost := func(err error) (tombstone.Tombstone, error) { return m.zipTombstone(err, artifact, url) }
	if err := s.download("artifact.zip", url, m.ArtifactMaxBytes, download, lost); err != nil {
		return err
	}
	// A zip that does not match its digest is Transient, so the next cycle downloads it again.
	if got, ok := s.sources["artifact.zip"]; ok && want != "" && got.SHA256 != want {
		return failure.Transient{Err: fmt.Errorf("%s: sha256 %s does not match digest sha256:%s", url, got.SHA256, want)}
	}
	return nil
}

// zipTombstone tombstones a zip whose download failed for good: GitHub lost
// it, or its stream exceeded ArtifactMaxBytes.
func (m *Mirror) zipTombstone(err error, artifact github.Artifact, url string) (tombstone.Tombstone, error) {
	if errors.Is(err, store.ErrTooLarge) {
		message := fmt.Sprintf("the zip exceeded artifact_max_bytes %d although size_in_bytes is %d", m.ArtifactMaxBytes, artifact.SizeInBytes)
		return tombstone.New("artifact.zip", url, tombstone.TooLarge, message, m.Clock.Now()), nil
	}
	return tombstone.FromZipError(err, artifact.ExpiresAt, m.Clock.Now())
}

// artifactFetch is an artifact's fetch.json. It holds the run's facts as
// listed, so filters match an artifact whose attempt is not on disk.
type artifactFetch struct {
	unitFetch
	RunStatusAtFetch string            `json:"run_status_at_fetch"`
	WorkflowID       int64             `json:"workflow_id"`
	WorkflowName     string            `json:"workflow_name"`
	Event            string            `json:"event"`
	PRNumbers        []int             `json:"pr_numbers"`
	DisplayTitle     string            `json:"display_title"`
	Sources          map[string]source `json:"sources"`
}

func (m *Mirror) writeArtifactFetch(s *staged, run github.Run) error {
	prs := make([]int, len(run.PullRequests))
	for i, pr := range run.PullRequests {
		prs[i] = pr.Number
	}
	return writeValue(s.unit, "fetch.json", artifactFetch{
		unitFetch:        m.unitFetch(run),
		RunStatusAtFetch: run.Status,
		WorkflowID:       run.WorkflowID,
		WorkflowName:     run.Name,
		Event:            run.Event,
		PRNumbers:        prs,
		DisplayTitle:     run.DisplayTitle,
		Sources:          s.sources,
	})
}

// retrySet is the run's artifacts that have no dir on disk: those in this
// cycle's listing, then in pending, then in each attempt's snapshot. A
// re-run of all jobs deletes artifacts, so one that failed transiently may
// be in no later listing.
func (m *Mirror) retrySet(run listedRun, pending []github.Artifact) ([]github.Artifact, error) {
	snapshots, err := m.snapshots(run.dir)
	if err != nil {
		return nil, err
	}
	var retry []github.Artifact
	seen := map[int64]bool{}
	for _, artifact := range slices.Concat(run.artifacts, pending, snapshots) {
		if seen[artifact.ID] {
			continue
		}
		seen[artifact.ID] = true
		done, err := m.Store.Has(layout.ArtifactDir(run.dir, artifact.ID, artifact.Name))
		if err != nil {
			return nil, err
		}
		if !done {
			retry = append(retry, artifact)
		}
	}
	return retry, nil
}

// snapshots gives the artifacts in each attempt's artifacts.json, oldest attempt first.
func (m *Mirror) snapshots(runDir string) ([]github.Artifact, error) {
	attempts, err := m.attemptsOnDisk(runDir)
	if err != nil {
		return nil, err
	}
	slices.Sort(attempts)
	var all []github.Artifact
	for _, n := range attempts {
		artifacts, err := readArtifacts(filepath.Join(layout.AttemptDir(runDir, n), "artifacts.json"))
		if err != nil {
			return nil, err
		}
		all = append(all, artifacts...)
	}
	return all, nil
}

// readArtifacts reads a JSON array of artifacts as GitHub listed them.
func readArtifacts(path string) ([]github.Artifact, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	artifacts := make([]github.Artifact, len(raws))
	for i, raw := range raws {
		artifacts[i].Raw = raw
		if err := json.Unmarshal(raw, &artifacts[i].Artifact); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return artifacts, nil
}
