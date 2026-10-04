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
	"github.com/rosenhouse/lg/internal/store"
)

// pending is state/pending-artifacts.json: each run's listed artifacts that
// have no dir yet, with their origins. A re-run of all jobs deletes a run's
// artifacts, so the next listing may not name one that failed.
type pending struct {
	store *store.Store
	path  string
	runs  map[int64]pendingRun
}

// pendingRun is a run as last listed, with its pending artifacts.
type pendingRun struct {
	Run       github.Run  `json:"run"`
	Artifacts []candidate `json:"artifacts"`
}

// loadPending reads the file. One that does not parse is moved aside, since
// snapshots still name the artifacts of published attempts, and loadPending
// gives an empty pending and the parse error as discarded.
func loadPending(s *store.Store) (p *pending, discarded, err error) {
	p = &pending{store: s, path: filepath.Join(s.State(), "pending-artifacts.json"), runs: map[int64]pendingRun{}}
	raw, err := os.ReadFile(p.path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var runs map[int64]pendingRun
	if err := decodePending(raw, &runs); err != nil {
		aside := p.path + ".corrupt"
		if err := s.Rename(p.path, aside); err != nil {
			return nil, nil, err
		}
		return p, &corruptFileError{Path: p.path, Err: fmt.Errorf("moved to %s: %w", aside, err)}, nil
	}
	if runs != nil {
		p.runs = runs
	}
	return p, nil, nil
}

func decodePending(raw []byte, runs *map[int64]pendingRun) error {
	if err := json.Unmarshal(raw, runs); err != nil {
		return err
	}
	for runID, r := range *runs {
		if r.Run.ID != runID {
			return fmt.Errorf("run %d has run id %d", runID, r.Run.ID)
		}
		if i := slices.IndexFunc(r.Artifacts, func(c candidate) bool { return c.Artifact.ID == 0 }); i >= 0 {
			return fmt.Errorf("run %d artifact %d has no id", runID, i)
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
		p.runs[run.ID] = pendingRun{Run: run, Artifacts: candidates}
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
	enc.SetIndent("", "  ")
	if err := enc.Encode(p.runs); err != nil {
		return err
	}
	return p.store.ReplaceFile(p.path, buf.Bytes())
}
