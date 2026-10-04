// Package mirror copies a repository's Actions data from GitHub into the store.
package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
	Tokens    auth.TokenSource
	NewGitHub func(token string) github.Client
	Store     *store.Store
	Host      string
	Repo      string
	Clock     clock.Clock
	LogGrace  time.Duration
}

// Cycle publishes attempt 1 of every listed run once it has completed. A
// Transient error or an error status aborts only its run's attempt; Cycle
// returns these after trying every other run. Any other error stops the cycle.
func (m *Mirror) Cycle(ctx context.Context) error {
	token, err := m.Tokens.Token(ctx, m.Host)
	if err != nil {
		return err
	}
	gh := m.NewGitHub(token)
	runs, err := gh.ListRuns(ctx)
	if err != nil {
		return err
	}
	var failed []error
	for _, run := range runs {
		// The API's spelling names the repo dir, so it must be the configured repo.
		if !strings.EqualFold(run.Repository.FullName, m.Repo) {
			return fmt.Errorf("run %d belongs to %q, not %q", run.ID, run.Repository.FullName, m.Repo)
		}
		runDir := layout.RunDir(layout.RepoDir(m.Store.Data(), m.Host, run.Repository.FullName), run.Run)
		target := layout.AttemptDir(runDir, 1)
		published, err := m.Store.Has(target)
		if err != nil {
			return errors.Join(append(failed, err)...)
		}
		if published {
			continue
		}
		err = m.publishAttempt(ctx, gh, run, 1, target)
		var transient failure.Transient
		var statusErr *github.StatusError
		switch {
		case errors.As(err, &transient), errors.As(err, &statusErr):
			failed = append(failed, fmt.Errorf("run %d attempt 1: %w", run.ID, err))
		case err != nil:
			return errors.Join(append(failed, err)...)
		}
	}
	return errors.Join(failed...)
}

func (m *Mirror) publishAttempt(ctx context.Context, gh github.Client, run github.Run, n int, target string) error {
	attempt, attemptSource, err := gh.GetAttempt(ctx, run.ID, n)
	if runGone(err) {
		return nil
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
	if runGone(err) {
		return nil
	}
	if err != nil {
		return err
	}
	unit, err := m.Store.NewUnit()
	if err != nil {
		return err
	}
	s := &staged{unit: unit, sources: map[string]source{}}
	err = m.stageAttempt(ctx, gh, s, run, attempt, attemptSource, jobs, jobsSource)
	if err == nil {
		err = m.Store.Publish(unit, target)
	}
	if err != nil {
		return errors.Join(err, unit.Abort())
	}
	return nil
}

func runGone(err error) bool {
	return errors.Is(err, github.ErrNotFound) || errors.Is(err, github.ErrGone)
}

func (m *Mirror) stageAttempt(ctx context.Context, gh github.Client, s *staged, run, attempt github.Run, attemptSource github.Source, jobs []github.Job, jobsSource github.Source) error {
	if err := s.writeJSON("attempt.json", attempt.Raw, attemptSource); err != nil {
		return err
	}
	if err := s.writeJSON("jobs.json", jsonArray(jobs), jobsSource); err != nil {
		return err
	}
	for _, job := range jobs {
		if err := m.addJob(ctx, gh, s, attempt, job); err != nil {
			return err
		}
	}
	return m.writeFetch(s, run, attempt)
}

func (m *Mirror) addJob(ctx context.Context, gh github.Client, s *staged, attempt github.Run, job github.Job) error {
	dir := layout.JobDir(".", job.ID, job.Name)
	if err := s.unit.WriteJSON(filepath.Join(dir, "job.json"), job.Raw); err != nil {
		return err
	}
	if model.Classify(job.Job) == model.NotApplicable {
		ts := tombstone.NeverProduced("log.txt", gh.JobLogURL(job.ID), "GitHub produces no log for a job with no steps and no runner", m.Clock.Now())
		return writeTombstone(s.unit, dir, ts)
	}
	log := filepath.Join(dir, "log.txt")
	w, err := s.unit.Create(log)
	if err != nil {
		return err
	}
	logSource, err := gh.DownloadJobLog(ctx, job.ID, w)
	if closeErr := w.Close(); closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if err == nil {
		return s.record(log, logSource)
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
	raw, err := json.Marshal(ts)
	if err != nil {
		return err
	}
	return unit.WriteJSON(filepath.Join(dir, ts.Target+".tombstone"), raw)
}

// fetch is fetch.json: how and when lg fetched its unit.
type fetch struct {
	LgFormat          int               `json:"lg_format"`
	LgVersion         string            `json:"lg_version"`
	FetchedAt         time.Time         `json:"fetched_at"`
	Host              string            `json:"host"`
	Repo              string            `json:"repo"`
	RunID             int64             `json:"run_id"`
	Attempt           int               `json:"attempt"`
	RunCreatedAt      time.Time         `json:"run_created_at"`
	RunAttemptAtFetch int               `json:"run_attempt_at_fetch"`
	Sources           map[string]source `json:"sources"`
}

type source struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	Pages  int    `json:"pages,omitempty"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func (m *Mirror) writeFetch(s *staged, run, attempt github.Run) error {
	raw, err := json.Marshal(fetch{
		LgFormat:          1,
		LgVersion:         version.Version,
		FetchedAt:         m.Clock.Now().UTC().Truncate(time.Second),
		Host:              m.Host,
		Repo:              run.Repository.FullName,
		RunID:             run.ID,
		Attempt:           attempt.RunAttempt,
		RunCreatedAt:      run.CreatedAt,
		RunAttemptAtFetch: run.RunAttempt,
		Sources:           s.sources,
	})
	if err != nil {
		return err
	}
	return s.unit.WriteJSON("fetch.json", raw)
}

// staged is a unit being staged with the sources of its files.
type staged struct {
	unit    *store.Unit
	sources map[string]source
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
	s.sources[filepath.ToSlash(name)] = source{URL: from.URL, Status: from.Status, Pages: from.Pages, Bytes: sum.Bytes, SHA256: sum.SHA256}
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
