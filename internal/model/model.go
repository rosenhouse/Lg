// Package model holds GitHub Actions entities and the rules lg applies to them.
package model

import "time"

// Run is a workflow run, as listed or as of one attempt.
type Run struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Path       string     `json:"path"`
	HeadBranch string     `json:"head_branch"`
	CreatedAt  time.Time  `json:"created_at"`
	Status     string     `json:"status"`
	RunAttempt int        `json:"run_attempt"`
	Repository Repository `json:"repository"`
}

type Repository struct {
	FullName string `json:"full_name"`
}

type Job struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	RunnerName *string `json:"runner_name"`
	Steps      []Step  `json:"steps"`
}

type Step struct {
	Name string `json:"name"`
}

// JobKind says whether a job produced a log.
type JobKind string

const (
	Ran           JobKind = "ran"
	NotApplicable JobKind = "not_applicable"
)

// Classify gives NotApplicable to a job with no steps and no runner, which
// GitHub never produces a log for.
func Classify(job Job) JobKind {
	if len(job.Steps) == 0 && job.RunnerName == nil {
		return NotApplicable
	}
	return Ran
}
