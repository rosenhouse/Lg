package mirror

import (
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/store"
)

// watch is state/watch.json: by host and run id, the runs lg listed that
// are not complete on disk. A run created before the backfill window
// appears in no listing once it completes, so the cycle gets each watched
// run that no listing named.
type watch struct {
	store *store.Store
	host  string
	hosts map[string]map[int64]watchedRun
	// runs are the host's runs. The cycle removes each one it resolves and
	// adds the runs it leaves incomplete.
	runs   map[int64]watchedRun
	loaded []int64
}

type watchedRun struct {
	CreatedAt time.Time `json:"created_at"`
}

func loadWatch(s *store.Store, host string) (w *watch, discarded, err error) {
	hosts := map[string]map[int64]watchedRun{}
	discarded, err = s.ReadState("watch.json", func(raw []byte) error {
		var decoded map[string]map[int64]watchedRun
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
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
		runs = map[int64]watchedRun{}
	}
	return &watch{store: s, host: host, hosts: hosts, runs: runs, loaded: slices.Sorted(maps.Keys(runs))}, discarded, nil
}

func (w *watch) add(run model.Run) {
	w.runs[run.ID] = watchedRun{CreatedAt: run.CreatedAt}
}

// prune drops each run that evicted reports for its created_at.
func (w *watch) prune(evicted func(createdAt time.Time) bool) {
	maps.DeleteFunc(w.runs, func(_ int64, run watchedRun) bool { return evicted(run.CreatedAt) })
}

// save writes the file when the host's run ids changed.
func (w *watch) save() error {
	if slices.Equal(slices.Sorted(maps.Keys(w.runs)), w.loaded) {
		return nil
	}
	w.hosts[w.host] = w.runs
	return w.store.WriteState("watch.json", w.hosts)
}
