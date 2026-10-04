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
// have no dir yet. A re-run of all jobs deletes a run's artifacts, so the
// next listing may not name one that failed.
type pending struct {
	store *store.Store
	path  string
	runs  map[int64][]github.Artifact
}

// loadPending reads the file. One that does not parse is moved aside, since
// snapshots still name the artifacts of published attempts, and loadPending
// returns a MalformedError with an empty pending.
func loadPending(s *store.Store, stateDir string) (*pending, error) {
	p := &pending{store: s, path: filepath.Join(stateDir, "pending-artifacts.json"), runs: map[int64][]github.Artifact{}}
	raw, err := os.ReadFile(p.path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &p.runs); err != nil {
		p.runs = map[int64][]github.Artifact{}
		aside := p.path + ".corrupt"
		if err := os.Rename(p.path, aside); err != nil {
			return nil, err
		}
		return p, fmt.Errorf("%s, moved to %s: %w", p.path, aside, &github.MalformedError{Err: err})
	}
	return p, nil
}

// set records the run's pending artifacts, and saves the file when their ids changed.
func (p *pending) set(runID int64, artifacts []github.Artifact) error {
	if slices.Equal(ids(p.runs[runID]), ids(artifacts)) {
		return nil
	}
	if len(artifacts) == 0 {
		delete(p.runs, runID)
	} else {
		p.runs[runID] = artifacts
	}
	return p.save()
}

func ids(artifacts []github.Artifact) []int64 {
	ids := make([]int64, len(artifacts))
	for i, a := range artifacts {
		ids[i] = a.ID
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
