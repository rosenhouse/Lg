// Package mirror copies a repository's Actions data from GitHub into the store.
package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
	"github.com/rosenhouse/lg/internal/version"
)

type Mirror struct {
	Tokens           auth.TokenSource
	NewGitHub        func(token string) github.Client
	Store            *store.Store
	State            string
	Host             string
	Repo             string
	Clock            clock.Clock
	LogGrace         time.Duration
	ArtifactMaxBytes int64
}

// Cycle publishes each listed artifact and completed attempt that is not on
// disk. It does every run's artifacts first, newest run first as GitHub lists
// them, because a re-run of all jobs deletes them. An error that runScoped
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
	if m.ArtifactMaxBytes < 1 {
		return fmt.Errorf("ArtifactMaxBytes must be at least 1, not %d", m.ArtifactMaxBytes)
	}
	if m.State == "" {
		return errors.New("State is not set")
	}
	token, err := m.Tokens.Token(ctx, m.Host)
	if err != nil {
		return err
	}
	gh := m.NewGitHub(token)
	repo, err := m.getRepo(ctx, gh)
	if err != nil {
		return err
	}
	p, pendingErr := loadPending(m.Store, m.State)
	if p == nil {
		return pendingErr
	}
	runs, err := m.listRuns(ctx, gh, repo, p)
	if err != nil {
		return err
	}
	artifactsFailed, err := m.artifactPhase(ctx, gh, runs, p)
	if err != nil {
		return err
	}
	attemptsFailed, err := m.attemptPhase(ctx, gh, runs)
	if err != nil {
		return err
	}
	return errors.Join(slices.Concat([]error{pendingErr}, artifactsFailed, attemptsFailed)...)
}

// listedRun is a listed run with its dir and this cycle's listing of its
// artifacts, which is nil when the listing failed or was not found.
type listedRun struct {
	github.Run
	dir       string
	artifacts *artifactListing
}

// artifactListing is a run's artifacts as one listing gave them.
type artifactListing struct {
	artifacts []github.Artifact
	origin    origin
}

func (l *artifactListing) candidates() []candidate {
	candidates := make([]candidate, len(l.artifacts))
	for i, a := range l.artifacts {
		candidates[i] = candidate{Artifact: a, Origin: l.origin}
	}
	return candidates
}

// listRuns lists the repo's runs, then each run with pending artifacts that
// the listing does not name, since it may have been deleted since.
func (m *Mirror) listRuns(ctx context.Context, gh github.Client, repo github.Repo, p *pending) ([]listedRun, error) {
	runs, err := gh.ListRuns(ctx)
	if err != nil {
		return nil, err
	}
	if i := slices.IndexFunc(runs, func(run github.Run) bool { return !ofRepo(run, repo) }); i >= 0 {
		return nil, fmt.Errorf("run %d belongs to %q, not %q", runs[i].ID, runs[i].Repository.FullName, repo.FullName)
	}
	for _, id := range slices.Backward(slices.Sorted(maps.Keys(p.runs))) {
		run := p.runs[id].Run
		listed := slices.ContainsFunc(runs, func(listed github.Run) bool { return listed.ID == id })
		if !listed && ofRepo(run, repo) {
			runs = append(runs, run)
		}
	}
	listed := make([]listedRun, len(runs))
	for i, run := range runs {
		dir, err := m.Store.FindRunDir(layout.RunDir(layout.RepoDir(m.Store.Data(), m.Host, repo.FullName), run.Run))
		if err != nil {
			return nil, err
		}
		listed[i] = listedRun{Run: run, dir: dir}
	}
	return listed, nil
}

func ofRepo(run github.Run, repo github.Repo) bool {
	return strings.EqualFold(run.Repository.FullName, repo.FullName)
}

// artifactPhase publishes every run's artifacts. It returns the errors that
// runScoped accepts, and stops at any other.
func (m *Mirror) artifactPhase(ctx context.Context, gh github.Client, runs []listedRun, p *pending) ([]error, error) {
	var failed []error
	for i := range runs {
		var runFailed []error
		var err error
		runs[i].artifacts, runFailed, err = m.syncArtifacts(ctx, gh, runs[i], p)
		if err != nil {
			return nil, err
		}
		failed = append(failed, runFailed...)
	}
	return failed, nil
}

// attemptPhase publishes the attempts of every run whose artifacts this cycle
// listed, since each attempt holds that listing. It returns the errors that
// runScoped accepts, and stops at any other.
func (m *Mirror) attemptPhase(ctx context.Context, gh github.Client, runs []listedRun) ([]error, error) {
	var failed []error
	for _, run := range runs {
		if run.artifacts == nil {
			continue
		}
		runFailed, err := m.syncAttempts(ctx, gh, run)
		if err != nil {
			return nil, err
		}
		failed = append(failed, runFailed...)
	}
	return failed, nil
}

// syncAttempts publishes the run's planned attempts. It returns the errors that
// runScoped accepts, and stops at any other.
func (m *Mirror) syncAttempts(ctx context.Context, gh github.Client, run listedRun) ([]error, error) {
	onDisk, err := m.attemptsOnDisk(run.dir)
	if err != nil {
		return nil, err
	}
	var failed []error
	for _, n := range Plan(run.Run, onDisk) {
		err := m.publishAttempt(ctx, gh, run, n, layout.AttemptDir(run.dir, n))
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

func (m *Mirror) publishAttempt(ctx context.Context, gh github.Client, run listedRun, n int, target string) error {
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
		err = s.writeJSON("jobs.json", jsonArray(jobs, func(j github.Job) json.RawMessage { return j.Raw }), jobsSource)
	}
	if err == nil {
		err = s.writeJSON("artifacts.json", jsonArray(run.artifacts.artifacts, func(a github.Artifact) json.RawMessage { return a.Raw }), run.artifacts.origin.source())
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
	download := func(w io.Writer) error { return gh.DownloadJobLog(ctx, job.ID, w) }
	lost := func(err error) (tombstone.Tombstone, error) {
		return tombstone.FromError(err, attempt.UpdatedAt, m.LogGrace, m.Clock.Now())
	}
	return s.download(filepath.Join(dir, "log.txt"), gh.JobLogURL(job.ID), store.Unlimited, download, lost)
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

// unitFetch is what every fetch.json records.
type unitFetch struct {
	LgFormat          int       `json:"lg_format"`
	LgVersion         string    `json:"lg_version"`
	FetchedAt         time.Time `json:"fetched_at"`
	Host              string    `json:"host"`
	Repo              string    `json:"repo"`
	RunID             int64     `json:"run_id"`
	RunCreatedAt      time.Time `json:"run_created_at"`
	RunAttemptAtFetch int       `json:"run_attempt_at_fetch"`
	RunStatusAtFetch  string    `json:"run_status_at_fetch"`
}

// unitFetch gives the run's facts, and its run_attempt and status as o read them.
func (m *Mirror) unitFetch(run github.Run, o origin) unitFetch {
	return unitFetch{
		LgFormat:          1,
		LgVersion:         version.Version,
		FetchedAt:         m.Clock.Now().UTC().Truncate(time.Second),
		Host:              m.Host,
		Repo:              run.Repository.FullName,
		RunID:             run.ID,
		RunCreatedAt:      run.CreatedAt,
		RunAttemptAtFetch: o.RunAttempt,
		RunStatusAtFetch:  o.RunStatus,
	}
}

type source struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	Pages  int    `json:"pages,omitempty"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func (m *Mirror) writeFetch(s *staged, run listedRun, attempt github.Run) error {
	return writeValue(s.unit, "fetch.json", fetch{
		unitFetch:          m.unitFetch(run.Run, run.artifacts.origin),
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

// download stages name from get. When get fails, it stages the tombstone
// that lost gives instead, or returns lost's error.
func (s *staged) download(name, url string, maxBytes int64, get func(io.Writer) error, lost func(error) (tombstone.Tombstone, error)) error {
	w, err := s.unit.Create(name, maxBytes)
	if err != nil {
		return err
	}
	err = get(w)
	if closeErr := w.Close(); closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if err == nil {
		return s.record(name, github.Source{URL: url})
	}
	ts, err := lost(err)
	if err != nil {
		return err
	}
	if err := s.unit.Remove(name); err != nil {
		return err
	}
	return writeTombstone(s.unit, filepath.Dir(name), ts)
}

// jsonArray joins the elements as served into one array, as if GitHub had sent a single page.
func jsonArray[T any](elements []T, raw func(T) json.RawMessage) []byte {
	raws := make([][]byte, len(elements))
	for i, e := range elements {
		raws[i] = raw(e)
	}
	return append(append([]byte("["), bytes.Join(raws, []byte(","))...), ']')
}
