package model

import (
	"cmp"
	"slices"
	"time"
)

// Snapshot is what an attempt on disk tells about the run's artifacts: its
// start, and the ids its artifacts.json listed while run_attempt was ListedAt.
type Snapshot struct {
	Attempt      int
	RunStartedAt time.Time
	ListedAt     int
	Listed       []int64
}

// Attribution says how Attribute found an artifact's attempt.
type Attribution string

const (
	ByListing   Attribution = "listing"
	ByTimestamp Attribution = "timestamp"
	Unknown     Attribution = "unknown"
)

// Attribute gives the attempt that uploaded an artifact. Attempt N lists it by
// listing-diff when attempt N-1 does not, provided each listed during its own
// attempt. Otherwise createdAt must fall in [run_started_at(N),
// run_started_at(N+1)), which needs attempt N+1 on disk unless no later
// attempt is known: latest is the highest run_attempt seen elsewhere.
func Attribute(id int64, createdAt time.Time, latest int, snapshots []Snapshot) (int, Attribution) {
	byAttempt := map[int]Snapshot{0: {}}
	for _, s := range snapshots {
		byAttempt[s.Attempt] = s
		latest = max(latest, s.Attempt)
	}
	sorted := slices.SortedFunc(slices.Values(snapshots), func(a, b Snapshot) int { return cmp.Compare(a.Attempt, b.Attempt) })
	for _, s := range sorted {
		prev, ok := byAttempt[s.Attempt-1]
		if ok && listedOwn(s) && listedOwn(prev) && slices.Contains(s.Listed, id) && !slices.Contains(prev.Listed, id) {
			return s.Attempt, ByListing
		}
	}
	for _, s := range sorted {
		if createdAt.Before(s.RunStartedAt) {
			continue
		}
		next, ok := byAttempt[s.Attempt+1]
		if ok && createdAt.Before(next.RunStartedAt) || !ok && latest == s.Attempt {
			return s.Attempt, ByTimestamp
		}
	}
	return 0, Unknown
}

func listedOwn(s Snapshot) bool { return s.ListedAt == s.Attempt }
