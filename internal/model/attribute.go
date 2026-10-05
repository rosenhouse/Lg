package model

import (
	"cmp"
	"slices"
	"time"
)

// Snapshot is what an attempt on disk tells about the run's artifacts: its
// start, and the ids its artifacts.json listed while run_attempt was ListedDuring.
type Snapshot struct {
	Attempt      int
	RunStartedAt time.Time
	ListedDuring int
	Listed       []int64
}

// Attribution says how Attribute found an artifact's attempt.
type Attribution string

const (
	ByListing   Attribution = "listing-diff"
	ByTimestamp Attribution = "timestamp"
	Unknown     Attribution = "unknown"
)

// Attribute gives the attempt that uploaded an artifact. Attempt N lists it by
// listing-diff when attempt N-1 does not, provided each listed during its own
// attempt. Otherwise createdAt must fall in [run_started_at(N),
// run_started_at(N+1)), which needs attempt N+1 on disk unless N is the last
// attempt known. No attempt after fetchedDuring can hold the artifact.
func Attribute(id int64, createdAt time.Time, fetchedDuring int, snapshots []Snapshot) (int, Attribution) {
	last := fetchedDuring
	// Before attempt 1, the run listed no artifacts.
	byAttempt := map[int]Snapshot{0: {}}
	for _, s := range snapshots {
		byAttempt[s.Attempt] = s
		last = max(last, s.Attempt)
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
		if ok && createdAt.Before(next.RunStartedAt) || !ok && last == s.Attempt {
			return s.Attempt, ByTimestamp
		}
	}
	return 0, Unknown
}

func listedOwn(s Snapshot) bool { return s.ListedDuring == s.Attempt }

// AttemptJobs is an attempt's jobs with its run_started_at, which Classify needs.
type AttemptJobs struct {
	RunStartedAt time.Time
	Jobs         []Job
}

// MatchOriginal finds the job that ran which a carried-forward job copies: the
// latest job of the earlier attempts with its name, started_at, completed_at
// and runner_name that is not itself carried forward.
func MatchOriginal(job Job, earlier []AttemptJobs) (Job, bool) {
	for _, attempt := range slices.Backward(earlier) {
		for _, candidate := range attempt.Jobs {
			if sameExecution(candidate, job) && Classify(candidate, attempt.RunStartedAt) != CarriedForward {
				return candidate, true
			}
		}
	}
	return Job{}, false
}

func sameExecution(a, b Job) bool {
	return a.Name == b.Name && sameTime(a.StartedAt, b.StartedAt) && sameTime(a.CompletedAt, b.CompletedAt) &&
		(a.RunnerName == nil) == (b.RunnerName == nil) && (a.RunnerName == nil || *a.RunnerName == *b.RunnerName)
}

func sameTime(a, b *time.Time) bool {
	return (a == nil) == (b == nil) && (a == nil || a.Equal(*b))
}
