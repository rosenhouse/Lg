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
}

// Cycle publishes attempt 1 of every listed run once it has completed.
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
	for _, run := range runs {
		// The API's spelling names the repo dir, so it must be the configured repo.
		if !strings.EqualFold(run.Repository.FullName, m.Repo) {
			return fmt.Errorf("run %d belongs to %q, not %q", run.ID, run.Repository.FullName, m.Repo)
		}
		runDir := layout.RunDir(layout.RepoDir(m.Store.Data(), m.Host, run.Repository.FullName), run.Run)
		target := layout.AttemptDir(runDir, 1)
		published, err := m.Store.Has(target)
		if err != nil {
			return err
		}
		if published {
			continue
		}
		if err := m.publishAttempt(ctx, gh, run.ID, 1, target); err != nil {
			return err
		}
	}
	return nil
}

func (m *Mirror) publishAttempt(ctx context.Context, gh github.Client, runID int64, n int, target string) error {
	attempt, err := gh.GetAttempt(ctx, runID, n)
	if err != nil {
		return err
	}
	// An attempt still running would be frozen with partial logs and with its
	// queued jobs tombstoned as not_applicable.
	if attempt.Status != "completed" {
		return nil
	}
	jobs, _, err := gh.ListAttemptJobs(ctx, runID, n)
	if err != nil {
		return err
	}
	unit, err := m.Store.NewUnit()
	if err != nil {
		return err
	}
	err = m.stageAttempt(ctx, gh, unit, attempt, jobs)
	if err == nil {
		err = m.Store.Publish(unit, target)
	}
	if err != nil {
		return errors.Join(err, unit.Abort())
	}
	return nil
}

func (m *Mirror) stageAttempt(ctx context.Context, gh github.Client, unit *store.Unit, attempt github.Run, jobs []github.Job) error {
	if err := unit.WriteJSON("attempt.json", attempt.Raw); err != nil {
		return err
	}
	if err := unit.WriteJSON("jobs.json", jsonArray(jobs)); err != nil {
		return err
	}
	for _, job := range jobs {
		if err := addJob(ctx, gh, unit, job); err != nil {
			return err
		}
	}
	return nil
}

func addJob(ctx context.Context, gh github.Client, unit *store.Unit, job github.Job) error {
	dir := layout.JobDir(".", job.ID, job.Name)
	if err := unit.WriteJSON(filepath.Join(dir, "job.json"), job.Raw); err != nil {
		return err
	}
	if model.Classify(job.Job) == model.NotApplicable {
		ts, err := json.Marshal(tombstone.New("log.txt", "not_applicable"))
		if err != nil {
			return err
		}
		return unit.WriteJSON(filepath.Join(dir, "log.txt.tombstone"), ts)
	}
	w, err := unit.Create(filepath.Join(dir, "log.txt"))
	if err != nil {
		return err
	}
	if _, err := gh.DownloadJobLog(ctx, job.ID, w); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// jsonArray joins the jobs as served into one array, as if GitHub had sent a single page.
func jsonArray(jobs []github.Job) []byte {
	raws := make([][]byte, len(jobs))
	for i, job := range jobs {
		raws[i] = job.Raw
	}
	return append(append([]byte("["), bytes.Join(raws, []byte(","))...), ']')
}
