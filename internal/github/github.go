// Package github reads GitHub Actions data through the REST API.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/version"
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

// BaseURL is the REST API root for host: api.github.com for github.com and
// /api/v3 on any other host. A non-empty apiURL overrides both.
func BaseURL(host, apiURL string) string {
	switch {
	case apiURL != "":
		return apiURL
	case host == "github.com":
		return "https://api.github.com"
	default:
		return "https://" + host + "/api/v3"
	}
}

type HTTP struct {
	client  *http.Client
	repoURL string
}

func NewHTTP(client *http.Client, baseURL, repo string) *HTTP {
	return &HTTP{client: client, repoURL: baseURL + "/repos/" + repo}
}

func (h *HTTP) ListRuns(ctx context.Context) ([]Run, error) {
	var listing struct {
		WorkflowRuns []json.RawMessage `json:"workflow_runs"`
	}
	if err := h.getJSON(ctx, "/actions/runs?per_page=100", &listing); err != nil {
		return nil, err
	}
	runs := make([]Run, len(listing.WorkflowRuns))
	for i, raw := range listing.WorkflowRuns {
		runs[i].Raw = raw
		if err := json.Unmarshal(raw, &runs[i].Run); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

func (h *HTTP) GetAttempt(ctx context.Context, runID int64, attempt int) (Run, error) {
	var run Run
	if err := h.getJSON(ctx, fmt.Sprintf("/actions/runs/%d/attempts/%d", runID, attempt), &run.Raw); err != nil {
		return Run{}, err
	}
	return run, json.Unmarshal(run.Raw, &run.Run)
}

func (h *HTTP) ListAttemptJobs(ctx context.Context, runID int64, attempt int) ([]Job, error) {
	var listing struct {
		Jobs []json.RawMessage `json:"jobs"`
	}
	if err := h.getJSON(ctx, fmt.Sprintf("/actions/runs/%d/attempts/%d/jobs?per_page=100", runID, attempt), &listing); err != nil {
		return nil, err
	}
	jobs := make([]Job, len(listing.Jobs))
	for i, raw := range listing.Jobs {
		jobs[i].Raw = raw
		if err := json.Unmarshal(raw, &jobs[i].Job); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

// DownloadJobLog copies the log's bytes to w, following GitHub's redirect to blob storage.
func (h *HTTP) DownloadJobLog(ctx context.Context, jobID int64, w io.Writer) error {
	return h.get(ctx, fmt.Sprintf("/actions/jobs/%d/logs", jobID), func(body io.Reader) error {
		_, err := io.Copy(w, body)
		return err
	})
}

func (h *HTTP) getJSON(ctx context.Context, path string, v any) error {
	return h.get(ctx, path, func(body io.Reader) error {
		return json.NewDecoder(body).Decode(v)
	})
}

func (h *HTTP) get(ctx context.Context, path string, read func(io.Reader) error) error {
	url := h.repoURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "lg/"+version.Version)
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	if err := read(resp.Body); err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	return nil
}
