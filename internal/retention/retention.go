// Package retention removes what is older than retention, and the oldest
// runs while data/ is over disk_cap.
package retention

import (
	"cmp"
	"context"
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

// Victims are what retention removes, in order.
type Victims struct {
	// Expired are runs in date dirs before the cutoff.
	Expired []string
	// Extracted and then Evicted are removed while data/ is over disk_cap.
	// Evicted also holds the kept runs that Horizon passes.
	Extracted []string
	Evicted   []Run
	// Horizon is what Execute raises state/horizon.json to, and zero when it
	// stays.
	Horizon Horizon
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
// over diskCap, counting only run dirs, it removes extracted/ trees and then whole runs, oldest
// first: by date dir, then run id. Discovery skips runs created at or before
// the horizon, so Plan also evicts each kept run that the stored horizon h,
// or one raised past the evicted runs, passes.
func Plan(runs []Run, h Horizon, cutoff string, diskCap int64) Victims {
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
	for _, r := range kept {
		if total <= diskCap {
			break
		}
		v.Evicted = append(v.Evicted, r)
		total -= r.Bytes
	}
	if newest := newestCreatedAt(v.Evicted); newest.After(h.At) {
		h.At, v.Horizon = newest, Horizon{At: newest}
	}
	for _, r := range kept[len(v.Evicted):] {
		if !r.CreatedAt.IsZero() && h.Skips(r.CreatedAt) {
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

// Find gives what retention removes from data/ at now, past the stored horizon h.
func Find(data string, h Horizon, now time.Time, retention time.Duration, diskCap int64) (Victims, error) {
	runs, err := Scan(data)
	if err != nil {
		return Victims{}, err
	}
	return Plan(runs, h, Cutoff(now, retention), diskCap), nil
}

// Retain executes what Find gives at now, printing each dir it removes. It
// reports a corrupt horizon after evicting. Callers hold state/write.lock.
func Retain(ctx context.Context, s *store.Store, now time.Time, retention time.Duration, diskCap int64, removed io.Writer) error {
	h, discarded, err := ReadHorizon(s)
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
		// WalkDir visits all of a dir before its next sibling, so a path
		// within any run is within the last run found.
		run := last(runs)
		if d.IsDir() {
			if r, ok := runAt(path, parts); ok {
				runs = append(runs, r)
			} else if isExtracted(parts) && run != nil && within(path, run.Dir) {
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
// removes empty date dirs. It writes the raised horizon before the evicted
// runs go, so that a crash part way leaves no evicted run for discovery to
// fetch again. It stops between evictions once ctx is done. Callers hold
// state/write.lock.
func Execute(ctx context.Context, s *store.Store, v Victims, removed io.Writer) error {
	evict := func(dir string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
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
	runs := v.Evicted
	if !v.Horizon.At.IsZero() {
		err := v.Horizon.Write(s)
		// On a full disk, a run must go first to make room for the horizon.
		// A crash before the horizon is written costs one re-download of it.
		for isFull(err) && len(runs) > 0 {
			if err := evict(runs[0].Dir); err != nil {
				return err
			}
			runs = runs[1:]
			err = v.Horizon.Write(s)
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
	return removeEmptyDates(s)
}

func isFull(err error) bool { return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) }

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
