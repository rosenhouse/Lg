package scenario

import "time"

// IntermittentRuns are the runs of Intermittent.
type IntermittentRuns struct {
	// Main are push runs on main, oldest first.
	Main []Run
	// Fork is a pull_request run from a fork's main.
	Fork Run
	// Cancelled is a push run on main whose first attempt was cancelled.
	Cancelled Run
}

func (i IntermittentRuns) All() []Run {
	return append([]Run{i.Fork, i.Cancelled}, i.Main...)
}

// Intermittent gives runs 1 to 6 on main, the fork run 7 and the cancelled run 8.
func Intermittent() IntermittentRuns {
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	var runs IntermittentRuns
	for id := int64(1); id <= 6; id++ {
		runs.Main = append(runs.Main, CloneAt(id, "after-attempt-1", at.Add(time.Duration(id)*Day)))
	}
	runs.Fork = CloneAt(7, "after-attempt-1", at)
	runs.Cancelled = CloneAt(8, "after-attempt-1", at)
	return runs
}
