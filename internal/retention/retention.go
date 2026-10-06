// Package retention removes what is older than retention, and the oldest
// runs while data/ is over disk_cap.
package retention

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/store"
)

// Run is a run dir under a date dir.
type Run struct {
	Dir string
	// Repo is its repo dir, as RepoKey names it.
	Repo  string
	Date  string
	ID    int64
	Bytes int64
	// Attempts are the numbers of its attempt dirs.
	Attempts []int
	// Artifacts are the ids of its artifact dirs.
	Artifacts []int64
	// CreatedAt is the run_created_at in a fetch.json of the run, and zero
	// when it has none.
	CreatedAt time.Time
	// Extracted are its artifacts' extracted/ trees.
	Extracted []Tree
}

type Tree struct {
	Dir   string
	Bytes int64
}

// Victims are what retention removes, in order.
type Victims struct {
	// Expired are runs in date dirs before the cutoff.
	Expired []string
	// Extracted and then Evicted are removed while the runs are over disk_cap.
	// Evicted also holds the kept runs that Horizons pass.
	Extracted []string
	Evicted   []string
	// Horizons is what Execute writes to state/horizon.json, and nil when it
	// stays.
	Horizons Horizons
}

func (v Victims) Dirs() []string { return slices.Concat(v.Expired, v.Extracted, v.Evicted) }

// Cutoff is the UTC date retention before now. Date dirs before it expire.
func Cutoff(now time.Time, retention time.Duration) string {
	return now.UTC().Add(-retention).Format(time.DateOnly)
}

// Expired reports whether retention removes a run created at createdAt,
// since its date dir is before the cutoff.
func Expired(createdAt, now time.Time, retention time.Duration) bool {
	return createdAt.UTC().Format(time.DateOnly) < Cutoff(now, retention)
}

// Plan expires the runs in date dirs before cutoff. Then, while the runs
// are over diskCap, it removes extracted/ trees and then whole runs, oldest
// first: by date dir, then run id. Discovery skips runs created at or before
// their repo dir's horizon, so Plan also evicts each kept run that its stored
// horizon in h, or one raised past the evicted runs, passes.
func Plan(runs []Run, h Horizons, cutoff string, diskCap int64) Victims {
	runs = slices.SortedFunc(slices.Values(runs), func(a, b Run) int {
		return cmp.Or(cmp.Compare(a.Date, b.Date), cmp.Compare(a.ID, b.ID), cmp.Compare(a.Dir, b.Dir))
	})
	var v Victims
	var total int64
	for _, r := range runs {
		total += r.Bytes
	}
	var kept []Run
	for _, r := range runs {
		if r.Date < cutoff {
			v.Expired = append(v.Expired, r.Dir)
			total -= r.Bytes
		} else {
			kept = append(kept, r)
		}
	}
	for i := range kept {
		for _, tree := range kept[i].Extracted {
			if total <= diskCap {
				break
			}
			v.Extracted = append(v.Extracted, tree.Dir)
			total -= tree.Bytes
			kept[i].Bytes -= tree.Bytes
		}
	}
	n := 0
	for ; n < len(kept) && total > diskCap; n++ {
		v.Evicted = append(v.Evicted, kept[n].Dir)
		total -= kept[n].Bytes
	}
	if raised := raise(h, kept[:n]); raised != nil {
		h, v.Horizons = raised, raised
	}
	for _, r := range kept[n:] {
		if !r.CreatedAt.IsZero() && h.Skips(r.Repo, r.CreatedAt) {
			v.Evicted = append(v.Evicted, r.Dir)
		}
	}
	return v
}

// raise gives h with each repo dir's horizon raised to the newest CreatedAt
// of its evicted runs, and nil when none is raised.
func raise(h Horizons, evicted []Run) Horizons {
	raised := Horizons{}
	maps.Copy(raised, h)
	changed := false
	for _, r := range evicted {
		if r.CreatedAt.After(raised[r.Repo]) {
			raised[r.Repo], changed = r.CreatedAt, true
		}
	}
	if !changed {
		return nil
	}
	return raised
}

// Find gives what retention removes from data/ at now, past the stored horizons h.
func Find(data string, h Horizons, now time.Time, retention time.Duration, diskCap int64) (Victims, error) {
	runs, err := Scan(data)
	if err != nil {
		return Victims{}, err
	}
	return Plan(runs, h, Cutoff(now, retention), diskCap), nil
}

// Retain executes what Find gives at now, calling removed with each dir it
// removes. It reports a corrupt horizon after evicting. Callers hold
// state/write.lock.
func Retain(ctx context.Context, s *store.Store, now time.Time, retention time.Duration, diskCap int64, removed func(dir string)) error {
	h, discarded, err := ReadHorizons(s)
	if err != nil {
		return err
	}
	v, err := Find(s.Data(), h, now, retention, diskCap)
	if err != nil {
		return err
	}
	return errors.Join(Execute(ctx, s, v, removed), discarded)
}

// Scan finds the run dirs under every data/<host>/<owner>/<repo>/runs/<date>/,
// with the apparent bytes of their regular files.
func Scan(data string) ([]Run, error) { return scan(data, filepath.WalkDir) }

func scan(data string, walk func(string, fs.WalkDirFunc) error) ([]Run, error) {
	var runs []Run
	err := walk(data, func(path string, d fs.DirEntry, err error) error {
		// Only eviction removes what is under data/.
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(data, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if d.Type()&fs.ModeSymlink != 0 && len(parts) <= dateDepth {
			return fmt.Errorf("%s is a symlink, which lg does not follow below %s", path, data)
		}
		// WalkDir visits all of a dir before its next sibling, so a path
		// within any run is within the last run found.
		run := last(runs)
		if d.IsDir() {
			switch r, ok := runAt(path, parts); {
			case ok:
				runs = append(runs, r)
			case run == nil || !within(path, run.Dir):
			case isAttempt(parts):
				n, _ := layout.AttemptNumber(parts[runDepth])
				run.Attempts = append(run.Attempts, n)
			case isExtracted(parts):
				run.Extracted = append(run.Extracted, Tree{Dir: path})
			}
			return nil
		}
		if run == nil || !within(path, run.Dir) || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		run.Bytes += info.Size()
		if tree := last(run.Extracted); tree != nil && within(path, tree.Dir) {
			tree.Bytes += info.Size()
		}
		return nil
	})
	for i := range runs {
		runs[i].CreatedAt = runCreatedAt(runs[i].Dir)
	}
	return runs, err
}

// dateDepth is the depth below data/ of <host>/<owner>/<repo>/runs/<date>,
// and runDepth of the run dirs in it.
const (
	dateDepth = 5
	runDepth  = dateDepth + 1
)

// runAt gives the run at path, when parts, its path below data/, are
// <host>/<owner>/<repo>/runs/<date>/<run>.
func runAt(path string, parts []string) (Run, bool) {
	if len(parts) != runDepth || parts[3] != "runs" {
		return Run{}, false
	}
	date := parts[dateDepth-1]
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return Run{}, false
	}
	idPart, _, _ := strings.Cut(parts[runDepth-1], "_")
	id, _ := strconv.ParseInt(idPart, 10, 64)
	return Run{Dir: path, Repo: strings.Join(parts[:3], "/"), Date: date, ID: id}, true
}

// isAttempt reports whether parts, a path below data/, are <run>/attempt-N.
func isAttempt(parts []string) bool {
	if len(parts) != runDepth+1 {
		return false
	}
	_, ok := layout.AttemptNumber(parts[runDepth])
	return ok
}

// isExtracted reports whether parts, a path below data/, are <run>/artifacts/<artifact>/extracted.
func isExtracted(parts []string) bool {
	return len(parts) == runDepth+3 && parts[runDepth] == "artifacts" && parts[runDepth+2] == "extracted"
}

func within(path, dir string) bool { return strings.HasPrefix(path, dir+string(filepath.Separator)) }

func last[T any](s []T) *T {
	if len(s) == 0 {
		return nil
	}
	return &s[len(s)-1]
}

// Execute evicts the victims in order, calling removed with each, and then
// removes empty date dirs. It writes the raised horizon before the evicted
// runs go, so that a crash part way leaves no evicted run for discovery to
// fetch again. It stops between evictions once ctx is done. Callers hold
// state/write.lock.
func Execute(ctx context.Context, s *store.Store, v Victims, removed func(dir string)) error {
	evict := func(dir string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Evict(dir); err != nil {
			return err
		}
		removed(dir)
		return nil
	}
	for _, dir := range slices.Concat(v.Expired, v.Extracted) {
		if err := evict(dir); err != nil {
			return err
		}
	}
	runs := v.Evicted
	if v.Horizons != nil {
		err := v.Horizons.Write(s)
		// On a full disk, a run must go first to make room for the horizon.
		// A crash before the horizon is written costs one re-download of it.
		for isFull(err) && len(runs) > 0 {
			if err := evict(runs[0]); err != nil {
				return err
			}
			runs = runs[1:]
			err = v.Horizons.Write(s)
		}
		if err != nil {
			return err
		}
	}
	for _, dir := range runs {
		if err := evict(dir); err != nil {
			return err
		}
	}
	return removeEmptyDates(s)
}

func isFull(err error) bool { return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) }

// runCreatedAt is the run_created_at in a fetch.json of the run, and zero
// when it has none.
func runCreatedAt(dir string) time.Time {
	attempts, _ := filepath.Glob(filepath.Join(dir, "attempt-*", "fetch.json"))
	artifacts, _ := filepath.Glob(filepath.Join(dir, "artifacts", "*", "fetch.json"))
	for _, path := range append(attempts, artifacts...) {
		if at := headerCreatedAt(path); !at.IsZero() {
			return at
		}
	}
	return time.Time{}
}

// headerCreatedAt reads the fetch.json at path only as far as its
// run_created_at, which comes before the sources that make it large.
func headerCreatedAt(path string) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
	if open, err := dec.Token(); err != nil || open != json.Delim('{') {
		return time.Time{}
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return time.Time{}
		}
		var value json.RawMessage
		if key != "run_created_at" {
			if dec.Decode(&value) != nil {
				return time.Time{}
			}
			continue
		}
		var at time.Time
		_ = dec.Decode(&at)
		return at
	}
	return time.Time{}
}

func removeEmptyDates(s *store.Store) error {
	dates, err := dateDirs(s.Data())
	if err != nil {
		return err
	}
	for _, date := range dates {
		if err := s.RemoveEmpty(date); err != nil {
			return err
		}
	}
	return nil
}

// dateDirs gives each dir at data/<host>/<owner>/<repo>/runs/<date>,
// following no symlink.
func dateDirs(data string) ([]string, error) {
	isAny := func(string) bool { return true }
	isDate := func(name string) bool {
		_, err := time.Parse(time.DateOnly, name)
		return err == nil
	}
	dirs := []string{data}
	for _, match := range []func(string) bool{isAny, isAny, isAny, isRuns, isDate} {
		var next []string
		for _, dir := range dirs {
			entries, err := os.ReadDir(dir)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if e.IsDir() && match(e.Name()) {
					next = append(next, filepath.Join(dir, e.Name()))
				}
			}
		}
		dirs = next
	}
	return dirs, nil
}

func isRuns(name string) bool { return name == "runs" }
