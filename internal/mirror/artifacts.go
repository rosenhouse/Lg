package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// origin is where an artifact object was listed: the listing's URL and
// pages, and the run's run_attempt and status read just after it, which are
// 0 and "" when lg did not read them.
type origin struct {
	URL        string `json:"url"`
	Pages      int    `json:"pages"`
	RunAttempt int    `json:"run_attempt"`
	RunStatus  string `json:"run_status"`
}

func (o origin) source() github.Source { return github.Source{URL: o.URL, Pages: o.Pages} }

// candidate is an artifact object to publish, with its origin.
type candidate struct {
	Artifact github.Artifact `json:"artifact"`
	Origin   origin          `json:"origin"`
}

// syncArtifacts lists the run's artifacts into run.artifacts, which stays nil
// when ListArtifacts fails, and publishes its retry set. A run complete on
// disk with nothing to retry needs no listing. It returns the errors that
// runScoped accepts, and stops at any other.
func (m *Mirror) syncArtifacts(ctx context.Context, gh github.Client, run *listedRun, p *pending) ([]UnitError, error) {
	onDisk, err := m.attemptsOnDisk(run.dir)
	if err != nil {
		return nil, err
	}
	run.planned = Plan(run.Run, onDisk)
	if run.Status == "completed" && len(run.planned) == 0 {
		retry, unreadable, err := m.retrySet(run.dir, onDisk, nil, p.runs[run.ID].Artifacts)
		if err != nil {
			return nil, err
		}
		if len(retry) == 0 && unreadable == nil {
			return nil, nil
		}
	}
	listing, err := m.listArtifacts(ctx, gh, run)
	var failed []UnitError
	switch {
	case errors.Is(err, github.ErrNotFound):
		// The run is deleted, but its artifacts that lg saw still need dirs.
	case runScoped(err) && listing != nil:
		failed = append(failed, UnitError{Run: run.ID, Err: err})
	case runScoped(err):
		return []UnitError{{Run: run.ID, Err: fmt.Errorf("artifacts: %w", err)}}, nil
	case err != nil:
		return nil, err
	}
	run.artifacts = listing
	retry, unreadable, err := m.retrySet(run.dir, onDisk, listing, p.runs[run.ID].Artifacts)
	if unreadable != nil {
		failed = append(failed, UnitError{Run: run.ID, Err: fmt.Errorf("artifacts: %w", unreadable)})
	}
	if err == nil {
		err = p.set(run.Run, retry)
	}
	if err != nil {
		return nil, err
	}
	var unpublished []candidate
	for _, c := range retry {
		err := m.publishArtifact(ctx, gh, run.Run, c, layout.ArtifactDir(run.dir, c.Artifact.ID, c.Artifact.Name))
		switch {
		case runScoped(err):
			failed = append(failed, UnitError{Run: run.ID, Artifact: c.Artifact.ID, Err: err})
			unpublished = append(unpublished, c)
		case err != nil:
			return failed, err
		}
	}
	return failed, p.set(run.Run, unpublished)
}

// listArtifacts lists the run's artifacts, then gets the run when needsRun,
// since a re-run may have started after ListRuns. When GetRun fails, it
// returns the listing with the error.
func (m *Mirror) listArtifacts(ctx context.Context, gh github.Client, run *listedRun) (*artifactListing, error) {
	artifacts, source, err := gh.ListArtifacts(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	listing := &artifactListing{artifacts: artifacts, origin: origin{URL: source.URL, Pages: source.Pages}}
	needed, err := m.needsRun(run, artifacts)
	if err != nil || !needed {
		return listing, err
	}
	got, err := gh.GetRun(ctx, run.ID)
	if err != nil {
		return listing, err
	}
	listing.origin.RunAttempt, listing.origin.RunStatus = got.RunAttempt, got.Status
	listing.runRead = true
	return listing, nil
}

// needsRun reports whether an attempt of the run is planned or a listed
// artifact has no dir, since publishing either records the run's run_attempt.
func (m *Mirror) needsRun(run *listedRun, artifacts []github.Artifact) (bool, error) {
	if len(run.planned) > 0 {
		return true, nil
	}
	for _, a := range artifacts {
		done, err := m.Store.Has(layout.ArtifactDir(run.dir, a.ID, a.Name))
		if err != nil {
			return false, err
		}
		if !done {
			return true, nil
		}
	}
	return false, nil
}

func (m *Mirror) publishArtifact(ctx context.Context, gh github.Client, run github.Run, c candidate, target string) error {
	unit, err := m.Store.NewUnit()
	if err != nil {
		return err
	}
	s := &staged{unit: unit, sources: map[string]source{}}
	err = s.writeJSON("artifact.json", c.Artifact.Raw, c.Origin.source())
	if err == nil {
		err = m.stageZip(ctx, gh, s, c.Artifact)
	}
	if err == nil {
		err = m.writeArtifactFetch(s, run, c.Origin)
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
	return tombstone.FromZipError(err, artifact.Artifact, m.LogGrace, m.Clock.Now())
}

// artifactFetch is an artifact's fetch.json. It holds the run's facts as
// listed, so filters match an artifact whose attempt is not on disk.
type artifactFetch struct {
	unitFetch
	WorkflowID   int64             `json:"workflow_id"`
	WorkflowName string            `json:"workflow_name"`
	Event        string            `json:"event"`
	PRNumbers    []int             `json:"pr_numbers"`
	DisplayTitle string            `json:"display_title"`
	Sources      map[string]source `json:"sources"`
}

func (m *Mirror) writeArtifactFetch(s *staged, run github.Run, o origin) error {
	prs := make([]int, len(run.PullRequests))
	for i, pr := range run.PullRequests {
		prs[i] = pr.Number
	}
	return writeValue(s.unit, "fetch.json", artifactFetch{
		unitFetch:    m.unitFetch(run, o),
		WorkflowID:   run.WorkflowID,
		WorkflowName: run.Name,
		Event:        run.Event,
		PRNumbers:    prs,
		DisplayTitle: run.DisplayTitle,
		Sources:      s.sources,
	})
}

// retrySet is the artifacts of the run in runDir that have no dir on disk:
// those in listing, then in pending, then in the snapshot of each attempt
// in onDisk. It skips the snapshots that do not parse and gives their errors
// as unreadable.
func (m *Mirror) retrySet(runDir string, onDisk []int, listing *artifactListing, pending []candidate) (retry []candidate, unreadable, err error) {
	snapshots, unreadable, err := snapshots(runDir, onDisk)
	if err != nil {
		return nil, nil, err
	}
	var listed []candidate
	if listing != nil {
		listed = listing.candidates()
	}
	seen := map[int64]bool{}
	for _, c := range slices.Concat(listed, pending, snapshots) {
		if seen[c.Artifact.ID] {
			continue
		}
		seen[c.Artifact.ID] = true
		done, err := m.Store.Has(layout.ArtifactDir(runDir, c.Artifact.ID, c.Artifact.Name))
		if err != nil {
			return nil, nil, err
		}
		if !done {
			retry = append(retry, c)
		}
	}
	return retry, unreadable, nil
}

// snapshots gives the artifacts in the artifacts.json of each attempt, oldest
// first. It skips the snapshots that do not parse and gives their errors as
// unreadable.
func snapshots(runDir string, attempts []int) (all []candidate, unreadable, err error) {
	for _, n := range slices.Sorted(slices.Values(attempts)) {
		snapshot, err := readSnapshot(layout.AttemptDir(runDir, n))
		var corrupt *store.CorruptFileError
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case errors.As(err, &corrupt):
			unreadable = errors.Join(unreadable, err)
		case err != nil:
			return nil, nil, err
		default:
			all = append(all, snapshot.candidates()...)
		}
	}
	return all, unreadable, nil
}

// readSnapshot reads an attempt's artifacts.json, with its origin from fetch.json.
func readSnapshot(attemptDir string) (*artifactListing, error) {
	var artifacts []github.Artifact
	if err := readJSON(filepath.Join(attemptDir, "artifacts.json"), &artifacts); err != nil {
		return nil, err
	}
	var f fetch
	if err := readJSON(filepath.Join(attemptDir, "fetch.json"), &f); err != nil {
		return nil, err
	}
	listed := f.Sources["artifacts.json"]
	return &artifactListing{
		artifacts: artifacts,
		origin:    origin{URL: listed.URL, Pages: listed.Pages, RunAttempt: f.RunAttemptAtFetch, RunStatus: f.RunStatusAtFetch},
	}, nil
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return &store.CorruptFileError{Path: path, Err: err}
	}
	return nil
}
