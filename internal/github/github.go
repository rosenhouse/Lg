// Package github reads GitHub Actions data through the REST API.
package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/rosenhouse/lg/internal/model"
)

// Run is a run with the body GitHub served for it.
type Run struct {
	model.Run
	Raw json.RawMessage
}

// Job is a job with the element GitHub served for it.
type Job struct {
	model.Job
	Raw json.RawMessage
}

type Client interface {
	ListRuns(ctx context.Context) ([]Run, error)
	GetAttempt(ctx context.Context, runID int64, attempt int) (Run, error)
	ListAttemptJobs(ctx context.Context, runID int64, attempt int) ([]Job, error)
	DownloadJobLog(ctx context.Context, jobID int64, w io.Writer) error
}

func BaseURL(string, string) string { return "" }

type HTTP struct{}

func NewHTTP(*http.Client, string, string) *HTTP { return &HTTP{} }

func (*HTTP) ListRuns(context.Context) ([]Run, error) { return nil, nil }

func (*HTTP) GetAttempt(context.Context, int64, int) (Run, error) { return Run{}, nil }

func (*HTTP) ListAttemptJobs(context.Context, int64, int) ([]Job, error) { return nil, nil }

func (*HTTP) DownloadJobLog(context.Context, int64, io.Writer) error { return nil }
