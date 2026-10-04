package mirror

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/store"
)

// watch is state/watch.json: by host and run id, the runs lg listed that
// are not complete on disk, as last listed. A run created before the
// backfill window appears in no listing once it completes, so the cycle gets
// each watched run that no listing named.
type watch struct {
	file  stateFile
	host  string
	hosts map[string]map[int64]model.Run
	// runs are the host's runs. The cycle removes each one it resolves and
	// adds the runs it leaves incomplete.
	runs   map[int64]model.Run
	loaded []int64
}

func loadWatch(s *store.Store, host string) (w *watch, discarded, err error) {
	file := newStateFile(s, "watch.json")
	hosts := map[string]map[int64]model.Run{}
	discarded, err = file.read(func(raw []byte) error {
		var decoded map[string]map[int64]model.Run
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		for host, runs := range decoded {
			for id, run := range runs {
				if run.ID != id {
					return fmt.Errorf("%s run %d has run id %d", host, id, run.ID)
				}
			}
		}
		if decoded != nil {
			hosts = decoded
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	runs := hosts[host]
	if runs == nil {
		runs = map[int64]model.Run{}
	}
	return &watch{file: file, host: host, hosts: hosts, runs: runs, loaded: slices.Sorted(maps.Keys(runs))}, discarded, nil
}

// prune drops the runs older than retention, which retention would evict.
func (w *watch) prune(now time.Time, retention time.Duration) {
	maps.DeleteFunc(w.runs, func(_ int64, run model.Run) bool { return run.CreatedAt.Before(now.Add(-retention)) })
}

// save writes the file when the host's run ids changed.
func (w *watch) save() error {
	if slices.Equal(slices.Sorted(maps.Keys(w.runs)), w.loaded) {
		return nil
	}
	w.hosts[w.host] = w.runs
	return w.file.write(w.hosts)
}
