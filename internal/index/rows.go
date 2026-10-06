package index

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/tombstone"
)

// Rows are a run's rows in every table. Paths are relative to the run dir.
type Rows struct {
	Run        Run
	Attempts   []Attempt
	Jobs       []Job
	Steps      []Step
	Artifacts  []Artifact
	Tombstones []Tombstone
	Units      []UnitDir
}

// UnitDir is a unit dir with its mtime in Unix nanoseconds. A unit published
// again under the same path has a new mtime.
type UnitDir struct {
	Path     string
	Modified int64
}

type Run struct {
	Host, Repo    string
	RunID         int64
	CreatedAt     time.Time
	DateDir       string
	WorkflowID    int64
	WorkflowName  string
	HeadBranch    string
	HeadSHA       string
	FromFork      bool
	Event         string
	PRNumbers     []int
	DisplayTitle  string
	LatestAttempt int
}

type Attempt struct {
	Attempt                   int
	Path                      string
	Status, Conclusion        string
	RunStartedAt, CompletedAt time.Time
}

type Job struct {
	JobID                  int64
	Attempt                int
	Name, Slug             string
	Kind                   model.JobKind
	OriginalJobID          int64
	Conclusion             string
	StartedAt, CompletedAt *time.Time
	RunnerName             *string
	Labels                 []string
	HasLog                 bool
	LogBytes               int64
	Path                   string
}

type Step struct {
	JobID                  int64
	Number                 int
	Name, Conclusion       string
	StartedAt, CompletedAt *time.Time
	Path                   string
}

type Artifact struct {
	ArtifactID        int64
	AttributedAttempt int
	Attribution       model.Attribution
	Name              string
	Size              int64
	CreatedAt         time.Time
	Expired, HasZip   bool
	Extracted         bool
	Path              string
}

type Tombstone struct {
	Path         string
	Reason       string
	HTTPStatus   *int
	TombstonedAt time.Time
}

// fetch is what the index reads of a unit's fetch.json. Only an artifact's
// holds the run's facts as listed.
type fetch struct {
	Host              string    `json:"host"`
	Repo              string    `json:"repo"`
	RunID             int64     `json:"run_id"`
	RunCreatedAt      time.Time `json:"run_created_at"`
	RunAttemptAtFetch int       `json:"run_attempt_at_fetch"`
	FetchedAt         time.Time `json:"fetched_at"`
	WorkflowID        int64     `json:"workflow_id"`
	WorkflowName      string    `json:"workflow_name"`
	Event             string    `json:"event"`
	PRNumbers         []int     `json:"pr_numbers"`
	DisplayTitle      string    `json:"display_title"`
}

type attemptFiles struct {
	n      int
	run    model.Run
	repos  model.RunRepositories
	jobs   []model.Job
	listed []model.Artifact
	fetch  fetch
}

type artifactFiles struct {
	dir      string
	artifact model.Artifact
	fetch    fetch
}

// IndexRun derives a run's rows from the files in runDir alone. It reads only
// the units it lists first, so a unit published later stays unindexed until
// the next Reconcile lists it.
func IndexRun(runDir string) (Rows, error) {
	units, err := runUnits(runDir)
	if err != nil {
		return Rows{}, err
	}
	var rows Rows
	var attempts []attemptFiles
	var artifacts []artifactFiles
	for _, unit := range units {
		rel, err := filepath.Rel(runDir, unit.Path)
		if err != nil {
			return Rows{}, err
		}
		rows.Units = append(rows.Units, UnitDir{Path: rel, Modified: unit.Modified})
		if n, ok := layout.AttemptNumber(rel); ok {
			a, err := readAttempt(unit.Path, n)
			if err != nil {
				return Rows{}, err
			}
			attempts = append(attempts, a)
		} else if filepath.Base(rel) != "extracted" {
			a, err := readArtifact(runDir, rel)
			if err != nil {
				return Rows{}, err
			}
			artifacts = append(artifacts, a)
		}
	}
	// Once retention evicts the run, its files read as absent.
	if _, err := os.Lstat(runDir); err != nil {
		return Rows{}, err
	}
	slices.SortFunc(attempts, func(a, b attemptFiles) int { return cmp.Compare(a.n, b.n) })
	b := &builder{dir: runDir, rows: &rows}
	rows.Run = runRow(attempts, artifacts)
	for i, a := range attempts {
		if err := b.addAttempt(a, attempts[:i]); err != nil {
			return Rows{}, err
		}
	}
	snapshots := snapshotsOf(attempts)
	for _, a := range artifacts {
		if err := b.addArtifact(a, snapshots); err != nil {
			return Rows{}, err
		}
	}
	return rows, nil
}

func readAttempt(dir string, n int) (attemptFiles, error) {
	a := attemptFiles{n: n}
	return a, errors.Join(
		readJSON(filepath.Join(dir, "attempt.json"), &a.run),
		readJSON(filepath.Join(dir, "attempt.json"), &a.repos),
		readJSON(filepath.Join(dir, "jobs.json"), &a.jobs),
		readJSON(filepath.Join(dir, "artifacts.json"), &a.listed),
		readJSON(filepath.Join(dir, "fetch.json"), &a.fetch))
}

func readArtifact(runDir, rel string) (artifactFiles, error) {
	a := artifactFiles{dir: rel}
	dir := filepath.Join(runDir, rel)
	return a, errors.Join(
		readJSON(filepath.Join(dir, "artifact.json"), &a.artifact),
		readJSON(filepath.Join(dir, "fetch.json"), &a.fetch))
}

// runRow takes the run's facts from its highest attempt, else from the
// artifact fetched last.
func runRow(attempts []attemptFiles, artifacts []artifactFiles) Run {
	if len(attempts) > 0 {
		latest := attempts[len(attempts)-1]
		var prs []int
		for _, a := range attempts {
			for _, pr := range a.run.PullRequests {
				prs = append(prs, pr.Number)
			}
		}
		return Run{
			Host: latest.fetch.Host, Repo: latest.fetch.Repo, RunID: latest.run.ID,
			CreatedAt: latest.fetch.RunCreatedAt, DateDir: latest.fetch.RunCreatedAt.UTC().Format(time.DateOnly),
			WorkflowID: latest.run.WorkflowID, WorkflowName: latest.run.Name,
			HeadBranch: latest.run.HeadBranch, HeadSHA: latest.run.HeadSHA, FromFork: latest.repos.FromFork(), Event: latest.run.Event,
			PRNumbers: union(prs), DisplayTitle: latest.run.DisplayTitle, LatestAttempt: latest.n,
		}
	}
	if len(artifacts) == 0 {
		return Run{}
	}
	last := slices.MaxFunc(artifacts, func(a, b artifactFiles) int {
		return cmp.Or(a.fetch.FetchedAt.Compare(b.fetch.FetchedAt), cmp.Compare(a.artifact.ID, b.artifact.ID))
	})
	var prs []int
	for _, a := range artifacts {
		prs = append(prs, a.fetch.PRNumbers...)
	}
	f := last.fetch
	return Run{
		Host: f.Host, Repo: f.Repo, RunID: f.RunID,
		CreatedAt: f.RunCreatedAt, DateDir: f.RunCreatedAt.UTC().Format(time.DateOnly),
		WorkflowID: f.WorkflowID, WorkflowName: f.WorkflowName,
		HeadBranch: last.artifact.WorkflowRun.HeadBranch, HeadSHA: last.artifact.WorkflowRun.HeadSHA,
		FromFork: last.artifact.WorkflowRun.FromFork(), Event: f.Event,
		PRNumbers: union(prs), DisplayTitle: f.DisplayTitle,
	}
}

func union(numbers []int) []int {
	slices.Sort(numbers)
	return slices.Compact(numbers)
}

// builder adds the rows of the run at dir.
type builder struct {
	dir  string
	rows *Rows
}

func (b *builder) addAttempt(a attemptFiles, earlier []attemptFiles) error {
	rows := b.rows
	dir := layout.AttemptDir("", a.n)
	rows.Attempts = append(rows.Attempts, Attempt{
		Attempt: a.n, Path: dir, Status: a.run.Status, Conclusion: a.run.Conclusion,
		RunStartedAt: a.run.RunStartedAt, CompletedAt: a.run.UpdatedAt,
	})
	var before []model.AttemptJobs
	for _, e := range earlier {
		before = append(before, model.AttemptJobs{RunStartedAt: e.run.RunStartedAt, Jobs: e.jobs})
	}
	for _, job := range a.jobs {
		jobDir := layout.JobDir(dir, job.ID, job.Name)
		row := Job{
			JobID: job.ID, Attempt: a.n, Name: job.Name, Slug: layout.Slug(job.Name),
			Kind: model.Classify(job, a.run.RunStartedAt), Conclusion: job.Conclusion,
			StartedAt: job.StartedAt, CompletedAt: job.CompletedAt, RunnerName: job.RunnerName,
			Labels: job.Labels, Path: jobDir,
		}
		if row.Kind == model.CarriedForward {
			if original, ok := model.MatchOriginal(job, before); ok {
				row.OriginalJobID = original.ID
			}
		}
		var err error
		row.HasLog, row.LogBytes, _, err = b.file(filepath.Join(jobDir, "log.txt"))
		if err != nil {
			return err
		}
		rows.Jobs = append(rows.Jobs, row)
		for _, step := range job.Steps {
			rows.Steps = append(rows.Steps, Step{
				JobID: job.ID, Number: step.Number, Name: step.Name, Conclusion: step.Conclusion,
				StartedAt: step.StartedAt, CompletedAt: step.CompletedAt, Path: jobDir,
			})
		}
	}
	return nil
}

func snapshotsOf(attempts []attemptFiles) []model.Snapshot {
	snapshots := make([]model.Snapshot, len(attempts))
	for i, at := range attempts {
		snapshots[i] = model.Snapshot{Attempt: at.n, RunStartedAt: at.run.RunStartedAt, ListedDuring: at.fetch.RunAttemptAtFetch}
		for _, listed := range at.listed {
			snapshots[i].Listed = append(snapshots[i].Listed, listed.ID)
		}
	}
	return snapshots
}

func (b *builder) addArtifact(a artifactFiles, snapshots []model.Snapshot) error {
	row := Artifact{
		ArtifactID: a.artifact.ID, Name: a.artifact.Name, Size: a.artifact.SizeInBytes,
		CreatedAt: a.artifact.CreatedAt, Path: a.dir,
	}
	row.AttributedAttempt, row.Attribution = model.Attribute(a.artifact.ID, a.artifact.CreatedAt, a.fetch.RunAttemptAtFetch, snapshots)
	hasZip, _, lost, err := b.file(filepath.Join(a.dir, "artifact.zip"))
	if err != nil {
		return err
	}
	row.HasZip, row.Expired = hasZip, lost == tombstone.Expired
	extracted := filepath.Join(a.dir, "extracted")
	row.Extracted = slices.ContainsFunc(b.rows.Units, func(u UnitDir) bool { return u.Path == extracted })
	b.rows.Artifacts = append(b.rows.Artifacts, row)
	return nil
}

// file reports whether name exists and its size. Otherwise it adds the row
// of its tombstone, if any, and gives the tombstone's reason.
func (b *builder) file(name string) (exists bool, size int64, lost tombstone.Reason, err error) {
	info, err := os.Lstat(filepath.Join(b.dir, name))
	if err == nil {
		return true, info.Size(), "", nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false, 0, "", err
	}
	var ts tombstone.Tombstone
	err = readJSON(filepath.Join(b.dir, name+".tombstone"), &ts)
	if errors.Is(err, fs.ErrNotExist) {
		return false, 0, "", nil
	}
	if err != nil {
		return false, 0, "", err
	}
	b.rows.Tombstones = append(b.rows.Tombstones, Tombstone{
		Path: name + ".tombstone", Reason: string(ts.Reason), HTTPStatus: ts.HTTPStatus, TombstonedAt: ts.TombstonedAt,
	})
	return false, 0, ts.Reason, nil
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
