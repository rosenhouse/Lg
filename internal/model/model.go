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
	RunAttempt int        `json:"run_attempt"`
	Repository Repository `json:"repository"`
}

type Repository struct {
	FullName string `json:"full_name"`
}
