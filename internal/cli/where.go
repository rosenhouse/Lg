package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/tombstone"
)

type whereCmd struct {
	Hits []string `arg:"" optional:"" name:"path|hit" help:"A path in the store, absolute or relative to it, or a line rg or grep printed. Without any, lines are read from stdin."`
}

func (whereCmd) Help() string {
	return "Prints one JSON object per input. It reads files only, never lg.db."
}

func (w whereCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	out := json.NewEncoder(deps.Stdout)
	out.SetEscapeHTML(false)
	finder := placeFinder{data: roots.Data, runs: map[string]runFacts{}}
	var failed []error
	err = eachInput(w.Hits, deps.Stdin, func(hit string) error {
		p, err := finder.find(hit)
		if err != nil {
			failed = append(failed, err)
			return nil
		}
		return out.Encode(p)
	})
	return errors.Join(append(failed, err)...)
}

// eachInput calls f with each of hits, or else with each line of stdin.
func eachInput(hits []string, stdin io.Reader, f func(string) error) error {
	if len(hits) > 0 || stdin == nil {
		for _, hit := range hits {
			if err := f(hit); err != nil {
				return err
			}
		}
		return nil
	}
	lines := bufio.NewReader(stdin)
	for {
		line, err := lines.ReadString('\n')
		if line = strings.TrimSuffix(line, "\n"); line != "" {
			if err := f(line); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// place is what lg where prints of a path or hit.
type place struct {
	Path    string `json:"path"`
	Host    string `json:"host"`
	Repo    string `json:"repo"`
	RunID   int64  `json:"run_id"`
	Attempt int    `json:"attempt,omitempty"`
	*jobPlace
	*artifactPlace
	Workflow  string    `json:"workflow"`
	Branch    string    `json:"branch"`
	SHA       string    `json:"sha"`
	Event     string    `json:"event"`
	PRs       []int     `json:"prs"`
	CreatedAt time.Time `json:"created_at"`
	*tombstonePlace
	Line    int     `json:"line,omitempty"`
	Text    string  `json:"text,omitempty"`
	HTMLURL *string `json:"html_url"`
}

type jobPlace struct {
	JobID          int64  `json:"job_id"`
	Job            string `json:"job"`
	JobConclusion  string `json:"job_conclusion"`
	CarriedForward bool   `json:"carried_forward"`
	OriginalJobID  int64  `json:"original_job_id,omitempty"`
	OriginalLog    string `json:"original_log,omitempty"`
}

type artifactPlace struct {
	ArtifactID        int64             `json:"artifact_id"`
	Artifact          string            `json:"artifact"`
	AttributedAttempt *int              `json:"attributed_attempt"`
	Attribution       model.Attribution `json:"attribution"`
}

type tombstonePlace struct {
	Reason     tombstone.Reason `json:"reason"`
	HTTPStatus *int             `json:"http_status"`
	Message    string           `json:"message"`
}

// runFacts are a run's rows, as index.IndexRun derives them from its files.
type runFacts struct {
	rows index.Rows
	err  error
}

// placeFinder decodes hits in the store at data, reading each run once.
type placeFinder struct {
	data string
	runs map[string]runFacts
}

func (f placeFinder) find(hit string) (place, error) {
	h, ok := layout.ParseHit(hit, func(path string) bool { _, ok := f.existing(path); return ok })
	if !ok {
		return place{}, fmt.Errorf("%s names no file", hit)
	}
	path, _ := f.existing(h.Path)
	rel, err := filepath.Rel(f.data, path)
	if err != nil || !filepath.IsLocal(rel) {
		return place{}, fmt.Errorf("%s is outside the store %s", path, f.data)
	}
	loc, err := layout.Parse(rel)
	if err != nil {
		return place{}, err
	}
	p, err := f.describe(loc)
	if err == nil && (loc.File == "log.txt.tombstone" || loc.File == "artifact.zip.tombstone") {
		p.tombstonePlace, err = tombstoneOf(path)
	}
	p.Path, p.Line, p.Text = path, h.Line, h.Text
	return p, err
}

// existing gives the file path names: path itself when absolute, else path
// relative to data/, else path relative to the working directory.
func (f placeFinder) existing(path string) (string, bool) {
	candidates := []string{path}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		candidates = []string{filepath.Join(f.data, path)}
		if err == nil {
			candidates = append(candidates, abs)
		}
	}
	for _, c := range candidates {
		if _, err := os.Lstat(c); err == nil {
			return filepath.Clean(c), true
		}
	}
	return "", false
}

func (f placeFinder) describe(loc layout.Location) (place, error) {
	runDir := filepath.Join(f.data, loc.RunDir)
	facts, ok := f.runs[runDir]
	if !ok {
		facts.rows, facts.err = index.IndexRun(runDir)
		f.runs[runDir] = facts
	}
	rows := facts.rows
	run := rows.Run
	p := place{
		Host: loc.Host, Repo: loc.Repo, RunID: loc.RunID, Attempt: loc.Attempt,
		Workflow: run.WorkflowName, Branch: run.HeadBranch, SHA: run.HeadSHA, Event: run.Event,
		PRs: append([]int{}, run.PRNumbers...), CreatedAt: run.CreatedAt,
	}
	unread := func(what string) error {
		return errors.Join(fmt.Errorf("%s: cannot read %s", filepath.Join(f.data, loc.RunDir), what), facts.err)
	}
	if run.RunID == 0 {
		return place{}, unread("the run")
	}
	var htmlFrom string
	switch {
	case loc.JobDir != "":
		htmlFrom = filepath.Join(f.data, loc.JobDir, "job.json")
	case loc.AttemptDir != "":
		htmlFrom = filepath.Join(f.data, loc.AttemptDir, "attempt.json")
	case run.LatestAttempt > 0:
		htmlFrom = filepath.Join(runDir, layout.AttemptDir("", run.LatestAttempt), "attempt.json")
	}
	if loc.JobDir != "" {
		i := slices.IndexFunc(rows.Jobs, func(j index.Job) bool { return j.JobID == loc.JobID && j.Attempt == loc.Attempt })
		if i < 0 {
			return place{}, unread("its job " + filepath.Base(loc.JobDir))
		}
		p.jobPlace = jobOf(runDir, rows, rows.Jobs[i])
	}
	if loc.ArtifactDir != "" {
		i := slices.IndexFunc(rows.Artifacts, func(a index.Artifact) bool { return a.ArtifactID == loc.ArtifactID })
		if i < 0 {
			return place{}, unread("its artifact " + filepath.Base(loc.ArtifactDir))
		}
		p.artifactPlace = artifactOf(rows.Artifacts[i])
	}
	if htmlFrom == "" {
		return p, nil
	}
	var err error
	p.HTMLURL, err = htmlURL(htmlFrom)
	return p, err
}

func jobOf(runDir string, rows index.Rows, job index.Job) *jobPlace {
	p := &jobPlace{JobID: job.JobID, Job: job.Name, JobConclusion: job.Conclusion, CarriedForward: job.Kind == model.CarriedForward}
	p.OriginalJobID = job.OriginalJobID
	i := slices.IndexFunc(rows.Jobs, func(j index.Job) bool { return j.JobID == job.OriginalJobID })
	if job.OriginalJobID != 0 && i >= 0 && rows.Jobs[i].HasLog {
		p.OriginalLog = filepath.Join(runDir, rows.Jobs[i].Path, "log.txt")
	}
	return p
}

func artifactOf(a index.Artifact) *artifactPlace {
	p := &artifactPlace{ArtifactID: a.ArtifactID, Artifact: a.Name, Attribution: a.Attribution}
	if a.AttributedAttempt > 0 {
		p.AttributedAttempt = &a.AttributedAttempt
	}
	return p
}

func htmlURL(path string) (*string, error) {
	var fields struct {
		HTMLURL *string `json:"html_url"`
	}
	return fields.HTMLURL, readJSONFile(path, &fields)
}

func tombstoneOf(path string) (*tombstonePlace, error) {
	var ts tombstone.Tombstone
	err := readJSONFile(path, &ts)
	return &tombstonePlace{Reason: ts.Reason, HTTPStatus: ts.HTTPStatus, Message: ts.Message}, err
}

func readJSONFile(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
