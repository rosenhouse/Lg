package model

import (
	"cmp"
	"slices"
	"time"
)

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

// FirstAttemptSeries gives the series of each job name, and of each step
// name of it, over the first attempts of runs. Only jobs that ran count. A
// name fails in a run if any of its jobs, or the step in any of them, failed;
// it succeeds if none failed and one succeeded. Series come by branch,
// workflow id and job name, each job before its steps, which come in the
// order they first ran.
func FirstAttemptSeries(jobs []FirstAttemptJob) []Series {
	ran := slices.DeleteFunc(slices.Clone(jobs), func(j FirstAttemptJob) bool { return j.Kind != Ran })
	slices.SortFunc(ran, func(a, b FirstAttemptJob) int {
		return cmp.Or(a.StartedAt.Compare(b.StartedAt), cmp.Compare(a.RunID, b.RunID), cmp.Compare(a.Job.ID, b.Job.ID))
	})
	type seriesKey struct {
		workflow          int64
		branch, job, step string
	}
	series := map[seriesKey]*Series{}
	var keys []seriesKey
	for _, j := range ran {
		concluded(j.AttemptJob, func(k flipKey, conclusion string) {
			if !failing(conclusion) && conclusion != "success" {
				return
			}
			key := seriesKey{j.WorkflowID, j.Branch, k.job, k.step}
			if series[key] == nil {
				series[key] = &Series{WorkflowID: j.WorkflowID, Branch: j.Branch, Job: k.job, Step: k.step}
				keys = append(keys, key)
			}
			series[key].observe(j, conclusion)
		})
	}
	out := make([]Series, len(keys))
	for i, k := range keys {
		out[i] = *series[k]
	}
	slices.SortStableFunc(out, func(a, b Series) int {
		return cmp.Or(cmp.Compare(a.Branch, b.Branch), cmp.Compare(a.WorkflowID, b.WorkflowID), cmp.Compare(a.Job, b.Job))
	})
	return out
}

// observe records how a job of a run, or the step of it, concluded.
func (s *Series) observe(j FirstAttemptJob, conclusion string) {
	s.Workflow = j.Workflow
	last := len(s.Runs) - 1
	switch {
	case last < 0 || s.Runs[last].RunID != j.RunID:
		s.Runs = append(s.Runs, RunOutcome{RunID: j.RunID, HeadSHA: j.HeadSHA, Conclusion: conclusion})
		last++
	case s.Runs[last].Conclusion == "success":
		s.Runs[last].Conclusion = conclusion
	}
	if failing(conclusion) {
		s.Runs[last].Logs = appendNew(s.Runs[last].Logs, j.Log)
	}
}

// IsolatedFailures gives the runs whose failure fell between two successes.
func (s Series) IsolatedFailures() []RunOutcome {
	return nil
}
