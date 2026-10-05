package model

import "time"

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

func Attribute(id int64, createdAt time.Time, latest int, snapshots []Snapshot) (int, Attribution) {
	return 0, Unknown
}
