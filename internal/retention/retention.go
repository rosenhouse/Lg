// Package retention removes what is older than retention, and the oldest
// runs while data/ is over disk_cap.
package retention

import (
	"cmp"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Run is a run dir under a date dir.
type Run struct {
	Dir   string
	Date  string
	ID    int64
	Bytes int64
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

// Plan expires the runs in date dirs before cutoff. Then, while data/ is
// over diskCap, it removes extracted/ trees and then whole runs, oldest
// first: by date dir, then run id.
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
	return v
}

// Scan finds the run dirs under every data/<host>/<owner>/<repo>/runs/<date>/,
// and sums the apparent bytes of the regular files under data/.
func Scan(data string) (Usage, error) {
	var u Usage
	err := filepath.WalkDir(data, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == data {
			return fs.SkipAll
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
	return u, err
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
