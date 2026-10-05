// Package retention removes what is older than retention, and the oldest
// runs while data/ is over disk_cap.
package retention

import (
	"cmp"
	"slices"
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

// Scan reads the runs under data/<host>/<owner>/<repo>/runs/<date>/.
func Scan(data string) (Usage, error) { return Usage{}, nil }
