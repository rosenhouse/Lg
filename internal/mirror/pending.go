package mirror

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/store"
)

// pending is state/pending-artifacts.json: by host and run id, each run's
// listed artifacts that have no dir yet, with their origins. A re-run of all
// jobs deletes a run's artifacts, so the next listing may not name one that
// failed.
type pending struct {
	store *store.Store
	path  string
	hosts map[string]map[int64]pendingRun
	// runs are the host's runs.
	runs map[int64]pendingRun
}

// pendingRun is the fields lg reads of a run as last listed, with its
// pending artifacts.
type pendingRun struct {
	Run       model.Run   `json:"run"`
	Artifacts []candidate `json:"artifacts"`
}

// loadPending reads the file and gives the host's runs, which are nil when
// the file has none for the host. One that does not parse is moved aside,
// and loadPending gives the parse error as discarded.
func loadPending(s *store.Store, host string) (p *pending, discarded, err error) {
	path := filepath.Join(s.State(), "pending-artifacts.json")
	hosts, discarded, err := readPending(s, path)
	if err != nil {
		return nil, nil, err
	}
	if hosts == nil {
		hosts = map[string]map[int64]pendingRun{}
	}
	return &pending{store: s, path: path, hosts: hosts, runs: hosts[host]}, discarded, nil
}

// restore records runs as the host's, and saves the file.
func (p *pending) restore(host string, runs map[int64]pendingRun) error {
	p.hosts[host], p.runs = runs, runs
	return p.save()
}

func readPending(s *store.Store, path string) (hosts map[string]map[int64]pendingRun, discarded, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if err := decodePending(raw, &hosts); err != nil {
		aside := path + ".corrupt"
		if err := s.Rename(path, aside); err != nil {
			return nil, nil, err
		}
		return nil, &corruptFileError{Path: path, Err: fmt.Errorf("moved to %s: %w", aside, err)}, nil
	}
	return hosts, nil, nil
}

func decodePending(raw []byte, hosts *map[string]map[int64]pendingRun) error {
	if err := json.Unmarshal(raw, hosts); err != nil {
		return err
	}
	for host, runs := range *hosts {
		for runID, r := range runs {
			if r.Run.ID != runID {
				return fmt.Errorf("%s run %d has run id %d", host, runID, r.Run.ID)
			}
			if i := slices.IndexFunc(r.Artifacts, func(c candidate) bool { return c.Artifact.ID == 0 }); i >= 0 {
				return fmt.Errorf("%s run %d: artifact #%d has no id", host, runID, i)
			}
		}
	}
	return nil
}

// set records the run's pending artifacts, and saves the file when their ids changed.
func (p *pending) set(run github.Run, candidates []candidate) error {
	if slices.Equal(ids(p.runs[run.ID].Artifacts), ids(candidates)) {
		return nil
	}
	if len(candidates) == 0 {
		delete(p.runs, run.ID)
	} else {
		p.runs[run.ID] = pendingRun{Run: run.Run, Artifacts: candidates}
	}
	return p.save()
}

func ids(candidates []candidate) []int64 {
	ids := make([]int64, len(candidates))
	for i, c := range candidates {
		ids[i] = c.Artifact.ID
	}
	return ids
}

func (p *pending) save() error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p.hosts); err != nil {
		return err
	}
	return p.store.ReplaceFile(p.path, buf.Bytes())
}

// loadPending loads the host's pending runs. When the file has none for the
// host, as when state/ was lost, it rebuilds them from the snapshots on disk
// of the repo's runs, since no listing may name those runs again.
func (m *Mirror) loadPending(repo github.Repo) (p *pending, discarded, err error) {
	p, discarded, err = loadPending(m.Store, m.Host)
	if err != nil || p.runs != nil {
		return p, discarded, err
	}
	runs, unreadable, err := m.pendingOnDisk(layout.RepoDir(m.Store.Data(), m.Host, repo.FullName))
	if err == nil {
		err = p.restore(m.Host, runs)
	}
	return p, errors.Join(discarded, unreadable), err
}

// pendingOnDisk gives each run under repoDir whose snapshots name artifacts
// with no dir. The run's fields come from its highest attempt's attempt.json,
// with created_at from its fetch.json, since an attempt's created_at is its own.
// It skips the files that do not parse and gives their errors as unreadable.
func (m *Mirror) pendingOnDisk(repoDir string) (runs map[int64]pendingRun, unreadable, err error) {
	runs = map[int64]pendingRun{}
	runsDir := filepath.Join(repoDir, "runs")
	dates, err := m.Store.Names(runsDir)
	if err != nil {
		return nil, nil, err
	}
	for _, date := range dates {
		names, err := m.Store.Names(filepath.Join(runsDir, date))
		if err != nil {
			return nil, nil, err
		}
		for _, name := range names {
			runDir := filepath.Join(runsDir, date, name)
			onDisk, err := m.attemptsOnDisk(runDir)
			if err != nil {
				return nil, nil, err
			}
			retry, runUnreadable, err := m.retrySet(runDir, onDisk, nil, nil)
			if err != nil {
				return nil, nil, err
			}
			unreadable = errors.Join(unreadable, runUnreadable)
			if len(retry) == 0 {
				continue
			}
			run, err := readRun(layout.AttemptDir(runDir, slices.Max(onDisk)))
			var corrupt *corruptFileError
			switch {
			case errors.As(err, &corrupt):
				unreadable = errors.Join(unreadable, err)
			case err != nil:
				return nil, nil, err
			default:
				runs[run.ID] = pendingRun{Run: run, Artifacts: retry}
			}
		}
	}
	return runs, unreadable, nil
}

// readRun reads the run as an attempt of it was fetched.
func readRun(attemptDir string) (model.Run, error) {
	var run model.Run
	if err := readJSON(filepath.Join(attemptDir, "attempt.json"), &run); err != nil {
		return model.Run{}, err
	}
	var f fetch
	if err := readJSON(filepath.Join(attemptDir, "fetch.json"), &f); err != nil {
		return model.Run{}, err
	}
	run.CreatedAt = f.RunCreatedAt
	return run, nil
}
