package scenario

import (
	"fmt"
	"strings"
	"time"
)

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

// Intermittent gives push runs 1 to 6 on main, a day apart. Their first
// attempts conclude integration SSFSSS, broken SSSFFF, steady SSSSSS and
// suite SFFFFF (S success, F failure), as suite's step unit goes SSSFSS and
// its step e2e SFF-FF (- skipped). Run 3 re-runs integration and suite,
// which succeed.
// Between runs 3 and 4, a fork's pull_request run 7 on main fails steady;
// between runs 4 and 5, run 8 is cancelled, steady with it.
func Intermittent() IntermittentRuns {
	first := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	run := func(id int64, at time.Time) Run {
		r := WithSHA(OnBranch(CloneAt(id, "after-attempt-1", at), "main"), strings.Repeat(fmt.Sprint(id), 40))
		renames := [][2]string{{"matrix (1.22)", "integration"}, {"matrix (1.23)", "broken"}, {"build (ubuntu-latest, 1.22)", "steady"}, {"pass", "suite"}}
		for _, rename := range renames {
			r = RenameJob(r, 1, rename[0], rename[1])
		}
		suite := r.JobIDs(1, "suite")[0]
		r = RenameStep(r, 1, suite, "Emit log markers", "unit")
		return RenameStep(r, 1, suite, "Build nested archives", "e2e")
	}
	conclude := func(r Run, job, step, conclusion string) Run {
		id := r.JobIDs(1, job)[0]
		if step == "" {
			return SetJobConclusion(r, 1, id, conclusion)
		}
		return SetStepConclusion(r, 1, id, step, conclusion)
	}
	outcomes := []struct{ job, step, letters string }{
		{"integration", "", "SSFSSS"},
		{"broken", "", "SSSFFF"},
		{"suite", "", "SFFFFF"},
		{"suite", "unit", "SSSFSS"},
		{"suite", "e2e", "SFF-FF"},
	}
	conclusions := map[byte]string{'F': "failure", '-': "skipped"}

	var runs IntermittentRuns
	for n := range 6 {
		r := run(int64(n+1), first.Add(time.Duration(n+1)*Day))
		for _, o := range outcomes {
			if conclusion, ok := conclusions[o.letters[n]]; ok {
				r = conclude(r, o.job, o.step, conclusion)
			}
		}
		runs.Main = append(runs.Main, r)
	}
	runs.Main[2] = AddRerunAttempt(runs.Main[2], "integration", "suite")

	fork := FromFork(WithEvent(run(7, first.Add(3*Day+6*time.Hour)), "pull_request"), "someone/Lg")
	runs.Fork = conclude(fork, "steady", "", "failure")
	cancelled := run(8, first.Add(4*Day+6*time.Hour)).conclude(1, "completed", "cancelled")
	runs.Cancelled = conclude(cancelled, "steady", "", "cancelled")
	return runs
}
