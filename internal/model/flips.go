package model

import (
	"cmp"
	"slices"
)

// AttemptJob is a job of one attempt of a run, with the path of its log, or "".
type AttemptJob struct {
	RunID   int64
	Attempt int
	Kind    JobKind
	Job     Job
	Log     string
}

// Outcome is how a job name, or a step of it, concluded in an attempt.
type Outcome struct {
	Attempt    int
	Conclusion string
}

// Flip is a job name of a run, or a step of it when Step is not "", that
// failed in one attempt and succeeded in another. FailingSteps are a job
// name's steps that failed in any attempt.
type Flip struct {
	RunID        int64
	Job, Step    string
	Outcomes     []Outcome
	FailingSteps []string
	Logs         []string
}

// RerunFlips gives each job name of a run, and each step name of it, that
// failed in one attempt and succeeded in another. Only jobs that ran count.
// A name fails in an attempt if any of its jobs, or the step in any of them,
// failed; it succeeds if none failed and one succeeded, unless the attempt
// carries forward a failure of it. Flips come by run and job name, each job
// before its steps, which come in the order they first ran.
func RerunFlips(jobs []AttemptJob) []Flip {
	type keyAttempt struct {
		flipKey
		attempt int
	}
	carriedFailures := map[keyAttempt]bool{}
	for _, j := range jobs {
		if j.Kind == CarriedForward {
			concluded(j, func(k flipKey, conclusion string) {
				if failing(conclusion) {
					carriedFailures[keyAttempt{k, j.Attempt}] = true
				}
			})
		}
	}
	ran := slices.DeleteFunc(slices.Clone(jobs), func(j AttemptJob) bool { return j.Kind != Ran })
	slices.SortFunc(ran, func(a, b AttemptJob) int {
		return cmp.Or(cmp.Compare(a.RunID, b.RunID), cmp.Compare(a.Attempt, b.Attempt), cmp.Compare(a.Job.ID, b.Job.ID))
	})
	flips := map[flipKey]*Flip{}
	var keys []flipKey
	flipOf := func(k flipKey) *Flip {
		if flips[k] == nil {
			flips[k] = &Flip{RunID: k.run, Job: k.job, Step: k.step}
			keys = append(keys, k)
		}
		return flips[k]
	}
	for _, j := range ran {
		concluded(j, func(k flipKey, conclusion string) {
			if conclusion == "success" && carriedFailures[keyAttempt{k, j.Attempt}] {
				conclusion = ""
			}
			flipOf(k).observe(j, conclusion)
			if k.step != "" && failing(conclusion) {
				job := flipOf(flipKey{k.run, k.job, ""})
				job.FailingSteps = appendNew(job.FailingSteps, k.step)
			}
		})
	}
	var out []Flip
	for _, k := range keys {
		if f := flips[k]; f.flipped() {
			out = append(out, *f)
		}
	}
	slices.SortStableFunc(out, func(a, b Flip) int { return cmp.Or(cmp.Compare(a.RunID, b.RunID), cmp.Compare(a.Job, b.Job)) })
	return out
}

// flipKey is a job name of a run, or a step name of it when step is not "".
type flipKey struct {
	run       int64
	job, step string
}

// concluded calls f with how job j, and each of its steps, concluded.
func concluded(j AttemptJob, f func(k flipKey, conclusion string)) {
	f(flipKey{j.RunID, j.Job.Name, ""}, j.Job.Conclusion)
	for _, step := range j.Job.Steps {
		f(flipKey{j.RunID, j.Job.Name, step.Name}, step.Conclusion)
	}
}

// observe records how a job of the run's attempt, or the step of it, concluded.
func (f *Flip) observe(j AttemptJob, conclusion string) {
	if j.Log != "" {
		f.Logs = appendNew(f.Logs, j.Log)
	}
	if !failing(conclusion) && conclusion != "success" {
		return
	}
	last := len(f.Outcomes) - 1
	switch {
	case last < 0 || f.Outcomes[last].Attempt != j.Attempt:
		f.Outcomes = append(f.Outcomes, Outcome{Attempt: j.Attempt, Conclusion: conclusion})
	case f.Outcomes[last].Conclusion == "success":
		f.Outcomes[last].Conclusion = conclusion
	}
}

func (f *Flip) flipped() bool {
	failed := slices.ContainsFunc(f.Outcomes, func(o Outcome) bool { return failing(o.Conclusion) })
	passed := slices.ContainsFunc(f.Outcomes, func(o Outcome) bool { return o.Conclusion == "success" })
	return failed && passed
}

func failing(conclusion string) bool {
	return conclusion == "failure" || conclusion == "cancelled" || conclusion == "timed_out"
}

// appendNew appends s unless list already holds it.
func appendNew(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}
