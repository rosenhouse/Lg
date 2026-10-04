// Package mirror copies a repository's Actions data from GitHub into the store.
package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/auth"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/tombstone"
)

type Mirror struct {
	Tokens    auth.TokenSource
	NewGitHub func(token string) github.Client
	Store     *store.Store
	Host      string
	Repo      string
	Clock     clock.Clock
	LogGrace  time.Duration

	ArtifactMaxBytes int64
}

// Cycle publishes every listed artifact, newest run first, and then each
// completed attempt of every listed run, that is not on disk. Artifacts go
// first because a re-run of all jobs deletes them. An error that runScoped
// accepts aborts only its artifact or attempt; Cycle returns these after
// trying every other. Any other error stops the cycle, and a local error that
// no retry fixes blocks it.
func (m *Mirror) Cycle(ctx context.Context) error {
	err := m.cycle(ctx)
	var blocked failure.Blocked
	if errors.As(err, &blocked) {
		if errors.Is(err, github.ErrUnauthorized) {
			blocked.Detail += fmt.Sprintf("; run `gh auth login --hostname %s`", m.Host)
		}
		return blocked
	}
	return failure.FromErrno(err)
}

func (m *Mirror) cycle(ctx context.Context) error {
	token, err := m.Tokens.Token(ctx, m.Host)
	if err != nil {
		return err
	}
	gh := m.NewGitHub(token)
	repo, err := m.getRepo(ctx, gh)
	if err != nil {
		return err
	}
	runs, err := gh.ListRuns(ctx)
	if err != nil {
		return err
	}
	dirs := make([]string, len(runs))
	for i, run := range runs {
		if !strings.EqualFold(run.Repository.FullName, repo.FullName) {
			return fmt.Errorf("run %d belongs to %q, not %q", run.ID, run.Repository.FullName, repo.FullName)
		}
		if dirs[i], err = m.Store.FindRunDir(layout.RunDir(layout.RepoDir(m.Store.Data(), m.Host, repo.FullName), run.Run)); err != nil {
			return err
		}
	}
	var failed []error
	for _, i := range newestFirst(runs) {
		runFailed, err := m.syncArtifacts(ctx, gh, dirs[i], runs[i])
		if err != nil {
			return err
		}
		failed = append(failed, runFailed...)
	}
	for i, run := range runs {
		runFailed, err := m.syncRun(ctx, gh, dirs[i], run)
		if err != nil {
			return err
		}
		failed = append(failed, runFailed...)
	}
	return errors.Join(failed...)
}

// newestFirst gives the indexes of runs, newest created first.
func newestFirst(runs []github.Run) []int {
	order := make([]int, len(runs))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return runs[b].CreatedAt.Compare(runs[a].CreatedAt) })
	return order
}

// syncRun publishes the run's planned attempts. It returns the errors that
// runScoped accepts, and stops at any other.
func (m *Mirror) syncRun(ctx context.Context, gh github.Client, runDir string, run github.Run) ([]error, error) {
	onDisk, err := m.attemptsOnDisk(runDir)
	if err != nil {
		return nil, err
	}
	var failed []error
	for _, n := range Plan(run, onDisk) {
		err := m.publishAttempt(ctx, gh, run, n, layout.AttemptDir(runDir, n))
		switch {
		case errors.Is(err, errRunGone):
			return failed, nil
		case runScoped(err):
			failed = append(failed, fmt.Errorf("run %d attempt %d: %w", run.ID, n, err))
		case err != nil:
			return nil, err
		}
	}
	return failed, nil
}

func (m *Mirror) attemptsOnDisk(runDir string) ([]int, error) {
	names, err := m.Store.Names(runDir)
	if err != nil {
		return nil, err
	}
	var onDisk []int
	for _, name := range names {
		if n, ok := layout.AttemptNumber(name); ok {
			onDisk = append(onDisk, n)
		}
	}
	return onDisk, nil
}

// getRepo gets the repo's full name, which names the repo dir as GitHub
// spells it, so a renamed repo gets a new dir.
func (m *Mirror) getRepo(ctx context.Context, gh github.Client) (github.Repo, error) {
	repo, err := gh.GetRepo(ctx)
	if errors.Is(err, github.ErrNotFound) {
		return github.Repo{}, failure.Blocked{Kind: failure.Auth, Detail: fmt.Sprintf("%s/%s was not found, or the token lacks access to it", m.Host, m.Repo)}
	}
	return repo, err
}

// runScoped reports whether err leaves other runs worth trying: GitHub
// failed this run, not lg's store, credentials or rate limit. A joined
// error must be run-scoped in every part.
func runScoped(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, part := range joined.Unwrap() {
			if !runScoped(part) {
				return false
			}
		}
		return true
	}
	var transient failure.Transient
	var statusErr *github.StatusError
	var malformed *github.MalformedError
	return errors.As(err, &transient) || errors.As(err, &statusErr) || errors.As(err, &malformed)
}

// errRunGone is a 404 on an attempt or its jobs, which skips the run.
var errRunGone = errors.New("run not found")

func (m *Mirror) publishAttempt(ctx context.Context, gh github.Client, run github.Run, n int, target string) error {
	attempt, attemptSource, err := gh.GetAttempt(ctx, run.ID, n)
	if errors.Is(err, github.ErrNotFound) {
		return errRunGone
	}
	if err != nil {
		return err
	}
	// An attempt still running would be frozen with partial logs and with its
	// queued jobs tombstoned as not_applicable.
	if attempt.Status != "completed" {
		return nil
	}
	jobs, jobsSource, err := gh.ListAttemptJobs(ctx, run.ID, n)
	if errors.Is(err, github.ErrNotFound) {
		return errRunGone
	}
	if err != nil {
		return err
	}
	// A completed attempt can list jobs that are not yet completed.
	if i := slices.IndexFunc(jobs, func(job github.Job) bool { return job.Status != "completed" }); i >= 0 {
		return failure.Transient{Err: fmt.Errorf("job %d is %s", jobs[i].ID, jobs[i].Status)}
	}
	unit, err := m.Store.NewUnit()
	if err != nil {
		return err
	}
	s := &staged{unit: unit, sources: map[string]source{}, carriedForward: []int64{}}
	err = s.writeJSON("attempt.json", attempt.Raw, attemptSource)
	if err == nil {
		err = s.writeJSON("jobs.json", jsonArray(jobs), jobsSource)
	}
	if err == nil {
		err = m.stageJobs(ctx, gh, s, attempt, jobs)
	}
	if err == nil {
		err = m.writeFetch(s, run, attempt)
	}
	if err == nil {
		err = m.Store.Publish(unit, target)
	}
	if err != nil {
		return errors.Join(err, unit.Abort())
	}
	return nil
}

func (m *Mirror) stageJobs(ctx context.Context, gh github.Client, s *staged, attempt github.Run, jobs []github.Job) error {
	for _, job := range jobs {
		if err := m.addJob(ctx, gh, s, attempt, job); err != nil {
			return err
		}
	}
	return nil
}

func (m *Mirror) addJob(ctx context.Context, gh github.Client, s *staged, attempt github.Run, job github.Job) error {
	dir := layout.JobDir(".", job.ID, job.Name)
	if err := s.unit.WriteJSON(filepath.Join(dir, "job.json"), job.Raw); err != nil {
		return err
	}
	switch model.Classify(job.Job, attempt.RunStartedAt) {
	case model.CarriedForward:
		s.carriedForward = append(s.carriedForward, job.ID)
		return nil
	case model.NotApplicable:
		ts := tombstone.New("log.txt", gh.JobLogURL(job.ID), tombstone.NotApplicable, "GitHub produces no log for a job with no steps and no runner", m.Clock.Now())
		return writeTombstone(s.unit, dir, ts)
	}
	log := filepath.Join(dir, "log.txt")
	w, err := s.unit.Create(log, store.Unlimited)
	if err != nil {
		return err
	}
	err = gh.DownloadJobLog(ctx, job.ID, w)
	if closeErr := w.Close(); closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if err == nil {
		return s.record(log, github.Source{URL: gh.JobLogURL(job.ID)})
	}
	ts, err := tombstone.FromError(err, attempt.UpdatedAt, m.LogGrace, m.Clock.Now())
	if err != nil {
		return err
	}
	if err := s.unit.Remove(log); err != nil {
		return err
	}
	return writeTombstone(s.unit, dir, ts)
}

func writeTombstone(unit *store.Unit, dir string, ts tombstone.Tombstone) error {
	return writeValue(unit, filepath.Join(dir, ts.Target+".tombstone"), ts)
}

// writeValue stores v as JSON with <, > and & as they are, so rg finds them.
func writeValue(unit *store.Unit, name string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return unit.WriteJSON(name, buf.Bytes())
}

// fetch is an attempt's fetch.json.
type fetch struct {
	unitFetch
	Attempt int               `json:"attempt"`
	Sources map[string]source `json:"sources"`
	// CarriedForwardJobs are the jobs whose logs are under the attempt that ran them.
	CarriedForwardJobs []int64 `json:"carried_forward_jobs"`
}

type source struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	Pages  int    `json:"pages,omitempty"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func (m *Mirror) writeFetch(s *staged, run, attempt github.Run) error {
	return writeValue(s.unit, "fetch.json", fetch{
		unitFetch:          m.unitFetch(run),
		Attempt:            attempt.RunAttempt,
		Sources:            s.sources,
		CarriedForwardJobs: s.carriedForward,
	})
}

// staged is a unit being staged with the sources of its files.
type staged struct {
	unit           *store.Unit
	sources        map[string]source
	carriedForward []int64
}

func (s *staged) writeJSON(name string, raw []byte, from github.Source) error {
	if err := s.unit.WriteJSON(name, raw); err != nil {
		return err
	}
	return s.record(name, from)
}

func (s *staged) record(name string, from github.Source) error {
	sum, err := s.unit.Sum(name)
	if err != nil {
		return err
	}
	s.sources[filepath.ToSlash(name)] = source{URL: from.URL, Status: http.StatusOK, Pages: from.Pages, Bytes: sum.Bytes, SHA256: sum.SHA256}
	return nil
}

// jsonArray joins the jobs as served into one array, as if GitHub had sent a single page.
func jsonArray(jobs []github.Job) []byte {
	raws := make([][]byte, len(jobs))
	for i, job := range jobs {
		raws[i] = job.Raw
	}
	return append(append([]byte("["), bytes.Join(raws, []byte(","))...), ']')
}
