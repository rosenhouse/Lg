package mirror

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/tombstone"
)

// syncArtifacts publishes the run's listed artifacts that are not on disk.
// It returns errRunGone for a run that is not found, and the errors that
// runScoped accepts, and stops at any other.
func (m *Mirror) syncArtifacts(ctx context.Context, gh github.Client, runDir string, run github.Run) ([]error, error) {
	artifacts, listing, err := gh.ListArtifacts(ctx, run.ID)
	if errors.Is(err, github.ErrNotFound) {
		return nil, errRunGone
	}
	if runScoped(err) {
		return []error{fmt.Errorf("run %d artifacts: %w", run.ID, err)}, nil
	}
	if err != nil {
		return nil, err
	}
	var failed []error
	for _, artifact := range artifacts {
		target := layout.ArtifactDir(runDir, artifact.ID, artifact.Name)
		done, err := m.Store.Has(target)
		if err == nil && !done {
			err = m.publishArtifact(ctx, gh, run, listing, artifact, target)
		}
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
