// Package model holds GitHub Actions entities and the rules lg applies to them.
package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Run is a workflow run, as listed or as of one attempt.
type Run struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	HeadBranch string    `json:"head_branch"`
	CreatedAt  time.Time `json:"created_at"`
	// RunStartedAt is when the latest attempt, or the one fetched, started.
	RunStartedAt time.Time     `json:"run_started_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	Status       string        `json:"status"`
	RunAttempt   int           `json:"run_attempt"`
	Repository   Repository    `json:"repository"`
	WorkflowID   int64         `json:"workflow_id"`
	Event        string        `json:"event"`
	PullRequests []PullRequest `json:"pull_requests"`
	DisplayTitle string        `json:"display_title"`
}

type PullRequest struct {
	Number int `json:"number"`
}

type Repository struct {
	FullName string `json:"full_name"`
}

type Job struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"started_at"`
	RunnerName *string    `json:"runner_name"`
	Steps      []Step     `json:"steps"`
}

type Step struct {
	Name string `json:"name"`
}

// JobKind says whether a job produced a log.
type JobKind string

const (
	Ran            JobKind = "ran"
	NotApplicable  JobKind = "not_applicable"
	CarriedForward JobKind = "carried_forward"
)

// Classify gives CarriedForward to a job that started before its attempt,
// which a rerun copies from the attempt that ran it. Otherwise it gives
// NotApplicable to a job with no steps and no runner, which GitHub never
// produces a log for.
func Classify(job Job, runStartedAt time.Time) JobKind {
	if job.StartedAt != nil && job.StartedAt.Before(runStartedAt) {
		return CarriedForward
	}
	if len(job.Steps) == 0 && job.RunnerName == nil {
		return NotApplicable
	}
	return Ran
}

// Artifact is an element of a run's artifacts listing.
type Artifact struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	SizeInBytes int64     `json:"size_in_bytes"`
	Expired     bool      `json:"expired"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Digest      string    `json:"digest"`
}

// ArtifactAction is what lg does with a listed artifact.
type ArtifactAction string

const (
	ArtifactExpired  ArtifactAction = "expired"
	ArtifactTooLarge ArtifactAction = "too_large"
	ArtifactDownload ArtifactAction = "download"
)

// ClassifyArtifact gives ArtifactExpired to an artifact listed as expired,
// else ArtifactTooLarge to one listed as larger than maxBytes.
func ClassifyArtifact(a Artifact, maxBytes int64) ArtifactAction {
	switch {
	case a.Expired:
		return ArtifactExpired
	case a.SizeInBytes > maxBytes:
		return ArtifactTooLarge
	}
	return ArtifactDownload
}

var digest = regexp.MustCompile(`^([a-zA-Z0-9]+):([0-9a-fA-F]+)$`)

// SHA256 gives the hex SHA-256 that the artifact's digest names, "" for a
// missing digest or one of another algorithm, and an error for a malformed one.
func (a Artifact) SHA256() (string, error) {
	if a.Digest == "" {
		return "", nil
	}
	m := digest.FindStringSubmatch(a.Digest)
	switch {
	case m == nil:
	case !strings.EqualFold(m[1], "sha256"):
		return "", nil
	case len(m[2]) == 64:
		return strings.ToLower(m[2]), nil
	}
	return "", fmt.Errorf("unrecognized digest %q", a.Digest)
}
