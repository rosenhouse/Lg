// Package retention removes what is older than retention, and the oldest
// runs while data/ is over disk_cap.
package retention

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rosenhouse/lg/internal/store"
)

// Run is a run dir under a date dir.
type Run struct {
	Dir   string
	Date  string
	ID    int64
	Bytes int64
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

// Usage is data/: its runs, and the apparent bytes of all its files.
type Usage struct {
	Runs  []Run
	Bytes int64
}

// Victims are what retention removes, in order.
type Victims struct {
	// Expired are runs in date dirs before the cutoff.
	Expired []string
	// Extracted and then Evicted are removed while data/ is over disk_cap.
	Extracted []string
	Evicted   []Run
}

func (v Victims) Dirs() []string {
	dirs := slices.Concat(v.Expired, v.Extracted)
	for _, r := range v.Evicted {
		dirs = append(dirs, r.Dir)
	}
	return dirs
}

// Cutoff is the UTC date retention before now. Date dirs before it expire.
func Cutoff(now time.Time, retention time.Duration) string {
	return now.UTC().Add(-retention).Format(time.DateOnly)
}

// Expired reports whether retention removes a run created at createdAt,
// since its date dir is before the cutoff.
func Expired(createdAt, now time.Time, retention time.Duration) bool {
	return createdAt.UTC().Format(time.DateOnly) < Cutoff(now, retention)
}

// Plan expires the runs in date dirs before cutoff. Then, while data/ is
// over diskCap, it removes extracted/ trees and then whole runs, oldest
// first: by date dir, then run id. It also evicts each kept run created at
// or before an evicted run.
func Plan(u Usage, cutoff string, diskCap int64) Victims {
	runs := slices.SortedFunc(slices.Values(u.Runs), func(a, b Run) int {
		return cmp.Or(cmp.Compare(a.Date, b.Date), cmp.Compare(a.ID, b.ID), cmp.Compare(a.Dir, b.Dir))
	})
	var v Victims
	total := u.Bytes
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
	for _, r := range kept {
		if total <= diskCap {
			break
		}
		v.Evicted = append(v.Evicted, r)
		total -= r.Bytes
	}
	// Discovery skips runs created at or before the horizon, so the kept
	// runs that the horizon would pass go too.
	horizon := newestCreatedAt(v.Evicted)
	for _, r := range kept[len(v.Evicted):] {
		if !r.CreatedAt.IsZero() && !r.CreatedAt.After(horizon) {
			v.Evicted = append(v.Evicted, r)
		}
	}
	return v
}

// newestCreatedAt is the newest CreatedAt of runs, and zero when none has one.
func newestCreatedAt(runs []Run) time.Time {
	var newest time.Time
	for _, r := range runs {
		if r.CreatedAt.After(newest) {
			newest = r.CreatedAt
		}
	}
	return newest
}

// Find gives what retention removes from data/ at now.
func Find(data string, now time.Time, retention time.Duration, diskCap int64) (Victims, error) {
	u, err := Scan(data)
	if err != nil {
		return Victims{}, err
	}
	return Plan(u, Cutoff(now, retention), diskCap), nil
}

// Scan finds the run dirs under every data/<host>/<owner>/<repo>/runs/<date>/,
// and sums the apparent bytes of the regular files under data/.
func Scan(data string) (Usage, error) {
	var u Usage
	err := walkDir(data, func(path string, d fs.DirEntry, err error) error {
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
		// WalkDir visits all of a dir before its next sibling, so a path
		// within any run is within the last run found.
		run := last(u.Runs)
		if d.IsDir() {
			if r, ok := runAt(path, parts); ok {
				u.Runs = append(u.Runs, r)
			} else if isExtracted(parts) && run != nil && within(path, run.Dir) {
				run.Extracted = append(run.Extracted, Tree{Dir: path})
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		u.Bytes += info.Size()
		if run != nil && within(path, run.Dir) {
			run.Bytes += info.Size()
			if tree := last(run.Extracted); tree != nil && within(path, tree.Dir) {
				tree.Bytes += info.Size()
			}
		}
		return nil
	})
	for i := range u.Runs {
		u.Runs[i].CreatedAt = runCreatedAt(u.Runs[i].Dir)
	}
	return u, err
}

var walkDir = filepath.WalkDir

// runAt gives the run at path, when parts, its path below data/, are
// <host>/<owner>/<repo>/runs/<date>/<run>.
func runAt(path string, parts []string) (Run, bool) {
	if len(parts) != 6 || parts[3] != "runs" {
		return Run{}, false
	}
	date := parts[4]
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return Run{}, false
	}
	idPart, _, _ := strings.Cut(parts[5], "_")
	id, _ := strconv.ParseInt(idPart, 10, 64)
	return Run{Dir: path, Date: date, ID: id}, true
}

// isExtracted reports whether parts, a path below data/, are <run>/artifacts/<artifact>/extracted.
func isExtracted(parts []string) bool {
	return len(parts) == 9 && parts[6] == "artifacts" && parts[8] == "extracted"
}

func within(path, dir string) bool { return strings.HasPrefix(path, dir+string(filepath.Separator)) }

func last[T any](s []T) *T {
	if len(s) == 0 {
		return nil
	}
	return &s[len(s)-1]
}

// Execute evicts the victims in order, printing each to removed, and then
// removes empty date dirs. It raises the horizon past the runs evicted for
// disk_cap before they go, so that a crash part way leaves no evicted run
// for discovery to fetch again. Callers hold state/write.lock.
func Execute(s *store.Store, v Victims, removed io.Writer) error {
	evict := func(dir string) error {
		if err := s.Evict(dir); err != nil {
			return err
		}
		_, err := fmt.Fprintln(removed, dir)
		return err
	}
	for _, dir := range slices.Concat(v.Expired, v.Extracted) {
		if err := evict(dir); err != nil {
			return err
		}
	}
	h, discarded, err := ReadHorizon(s)
	if err != nil {
		return err
	}
	raised := h
	if newest := newestCreatedAt(v.Evicted); newest.After(raised.At) {
		raised.At = newest
	}
	runs := v.Evicted
	if raised != h {
		err := raised.Write(s)
		// On a full disk, a run must go first to make room for the horizon.
		// A crash before the horizon is written costs one re-download of it.
		for errors.Is(err, syscall.ENOSPC) && len(runs) > 0 {
			if err := evict(runs[0].Dir); err != nil {
				return err
			}
			runs = runs[1:]
			err = raised.Write(s)
		}
		if err != nil {
			return err
		}
	}
	for _, r := range runs {
		if err := evict(r.Dir); err != nil {
			return err
		}
	}
	return errors.Join(removeEmptyDates(s), discarded)
}

// runCreatedAt is the run_created_at in a fetch.json of the run, and zero
// when it has none.
func runCreatedAt(dir string) time.Time {
	attempts, _ := filepath.Glob(filepath.Join(dir, "attempt-*", "fetch.json"))
	artifacts, _ := filepath.Glob(filepath.Join(dir, "artifacts", "*", "fetch.json"))
	for _, path := range append(attempts, artifacts...) {
		var f struct {
			RunCreatedAt time.Time `json:"run_created_at"`
		}
		raw, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(raw, &f) == nil && !f.RunCreatedAt.IsZero() {
			return f.RunCreatedAt
		}
	}
	return time.Time{}
}

func removeEmptyDates(s *store.Store) error {
	dates, err := filepath.Glob(filepath.Join(s.Data(), "*", "*", "*", "runs", "*"))
	if err != nil {
		return err
	}
	for _, date := range dates {
		info, err := os.Lstat(date)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			continue
		}
		if err := s.RemoveEmpty(date); err != nil {
			return err
		}
	}
	return nil
}
