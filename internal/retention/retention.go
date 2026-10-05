// Package retention removes what is older than retention, and the oldest
// runs while data/ is over disk_cap.
package retention

import "time"

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

func (v Victims) Dirs() []string { return nil }

func Cutoff(now time.Time, retention time.Duration) string { return "" }

func Plan(u Usage, cutoff string, diskCap int64) Victims { return Victims{} }
