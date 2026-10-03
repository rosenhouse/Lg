// Package mirror copies a repository's Actions data from GitHub into the store.
package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/tombstone"
)

type Mirror struct {
	GitHub github.Client
	Store  *store.Store
	Data   string
	Host   string
	Repo   string
}

// Cycle publishes attempt 1 of every listed run.
func (m *Mirror) Cycle(ctx context.Context) error {
	runs, err := m.GitHub.ListRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		runDir := layout.RunDir(layout.RepoDir(m.Data, m.Host, run.Repository.FullName), run.Run)
		if err := m.publishAttempt(ctx, run.ID, 1, layout.AttemptDir(runDir, 1)); err != nil {
			return err
		}
	}
	return nil
}

func (m *Mirror) publishAttempt(ctx context.Context, runID int64, n int, target string) error {
	attempt, err := m.GitHub.GetAttempt(ctx, runID, n)
	if err != nil {
		return err
	}
	jobs, err := m.GitHub.ListAttemptJobs(ctx, runID, n)
	if err != nil {
		return err
	}
	unit, err := m.Store.NewUnit()
	if err != nil {
		return err
	}
	if err := unit.WriteJSON("attempt.json", attempt.Raw); err != nil {
		return err
	}
	if err := unit.WriteJSON("jobs.json", jsonArray(jobs)); err != nil {
		return err
	}
	for _, job := range jobs {
		if err := m.addJob(ctx, unit, job); err != nil {
			return err
		}
	}
	return m.Store.Publish(unit, target)
}

func (m *Mirror) addJob(ctx context.Context, unit *store.Unit, job github.Job) error {
	dir := layout.JobDir("", job.ID, job.Name)
	if err := unit.WriteJSON(filepath.Join(dir, "job.json"), job.Raw); err != nil {
		return err
	}
	if model.Classify(job.Job) == model.NotApplicable {
		ts, err := json.Marshal(tombstone.New("log.txt", tombstone.NotApplicable))
		if err != nil {
			return err
		}
		return unit.WriteJSON(filepath.Join(dir, "log.txt.tombstone"), ts)
	}
	w, err := unit.Create(filepath.Join(dir, "log.txt"))
	if err != nil {
		return err
	}
	if err := m.GitHub.DownloadJobLog(ctx, job.ID, w); err != nil {
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
