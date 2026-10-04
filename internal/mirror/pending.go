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

// loadPending reads the file and gives the host's runs. One that does not
// parse is moved aside, since snapshots still name the artifacts of published
// attempts, and loadPending gives an empty pending and the parse error as
// discarded.
func loadPending(s *store.Store, host string) (p *pending, discarded, err error) {
	path := filepath.Join(s.State(), "pending-artifacts.json")
	hosts, discarded, err := readPending(s, path)
	if err != nil {
		return nil, nil, err
	}
	if hosts == nil {
		hosts = map[string]map[int64]pendingRun{}
	}
	if hosts[host] == nil {
		hosts[host] = map[int64]pendingRun{}
	}
	return &pending{store: s, path: path, hosts: hosts, runs: hosts[host]}, discarded, nil
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
