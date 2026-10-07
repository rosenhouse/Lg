package model

import "time"

// IsolatedFailures gives the index of each failing conclusion whose
// neighbours both succeeded.
func IsolatedFailures(conclusions []string) []int {
	var isolated []int
	for i := 1; i < len(conclusions)-1; i++ {
		if failing(conclusions[i]) && conclusions[i-1] == "success" && conclusions[i+1] == "success" {
			isolated = append(isolated, i)
		}
	}
	return isolated
}

// FirstAttemptJob is a job of a run's first attempt, with what places the
// run in a series.
type FirstAttemptJob struct {
	AttemptJob
	HeadSHA    string
	WorkflowID int64
	Workflow   string
	Branch     string
	// StartedAt is the first attempt's run_started_at.
	StartedAt time.Time
}

// Series is how a workflow's job name on a branch, or a step of it when Step
// is not "", concluded in the first attempt of each run, oldest first.
// Workflow is the name the latest run gives the workflow.
type Series struct {
	WorkflowID                  int64
	Workflow, Branch, Job, Step string
	Runs                        []RunOutcome
}

// RunOutcome is how a series concluded in a run, with the logs of its jobs that failed.
type RunOutcome struct {
	RunID      int64
	HeadSHA    string
	Conclusion string
	Logs       []string
}

func FirstAttemptSeries(jobs []FirstAttemptJob) []Series {
	return nil
}
