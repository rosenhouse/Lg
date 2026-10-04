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
)

// pending is state/pending-artifacts.json: each run's listed artifacts that
// have no dir yet. A re-run of all jobs deletes a run's artifacts, so the
// next listing may not name one that failed.
type pending struct {
	path string
	runs map[int64][]github.Artifact
}

func loadPending(stateDir string) (*pending, error) {
	p := &pending{path: filepath.Join(stateDir, "pending-artifacts.json"), runs: map[int64][]github.Artifact{}}
	raw, err := os.ReadFile(p.path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var runs map[int64][]json.RawMessage
	if err := json.Unmarshal(raw, &runs); err != nil {
		return nil, fmt.Errorf("%s: %w", p.path, err)
	}
	for runID, raws := range runs {
		artifacts, err := decodeArtifacts(raws)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.path, err)
		}
		p.runs[runID] = artifacts
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

// save replaces the file whole, so a crash leaves the old one or the new one.
func (p *pending) save() error {
	runs := map[int64][]json.RawMessage{}
	for runID, artifacts := range p.runs {
		for _, a := range artifacts {
			runs[runID] = append(runs[runID], a.Raw)
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(runs); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.path), ".pending-artifacts-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(buf.Bytes())
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p.path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}
