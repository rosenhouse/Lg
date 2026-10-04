// Package github reads GitHub Actions data through the REST API.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"

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
	api     neturl.URL
	repoURL string
	token   string
}

func NewHTTP(client *http.Client, baseURL, repo, token string) *HTTP {
	h := &HTTP{repoURL: baseURL + "/repos/" + repo, token: token}
	if api, err := neturl.Parse(baseURL); err == nil {
		h.api = *api
	}
	withRedirects := *client
	withRedirects.CheckRedirect = h.checkRedirect
	h.client = &withRedirects
	return h
}

// checkRedirect keeps the token on the API host. Go's own rule would send it
// to a blob host that differs only by port or is a subdomain.
func (h *HTTP) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if !h.onAPIHost(req.URL) {
		req.Header.Del("Authorization")
	}
	return nil
}

func (h *HTTP) ListRuns(ctx context.Context) ([]Run, error) {
	raws, _, err := h.list(ctx, "/actions/runs?per_page=100", "workflow_runs")
	if err != nil {
		return nil, err
	}
	runs := make([]Run, len(raws))
	for i, raw := range raws {
		runs[i].Raw = raw
		if err := json.Unmarshal(raw, &runs[i].Run); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

func (h *HTTP) GetAttempt(ctx context.Context, runID int64, attempt int) (Run, error) {
	var run Run
	if err := h.getJSON(ctx, h.repoURL+fmt.Sprintf("/actions/runs/%d/attempts/%d", runID, attempt), &run.Raw); err != nil {
		return Run{}, err
	}
	err := json.Unmarshal(run.Raw, &run.Run)
	return run, err
}

func (h *HTTP) ListAttemptJobs(ctx context.Context, runID int64, attempt int) ([]Job, error) {
	path := fmt.Sprintf("/actions/runs/%d/attempts/%d/jobs?per_page=100", runID, attempt)
	raws, total, err := h.list(ctx, path, "jobs")
	if err != nil {
		return nil, err
	}
	if total > len(raws) {
		return nil, fmt.Errorf("%s: listed %d of %d jobs", h.repoURL+path, len(raws), total)
	}
	jobs := make([]Job, len(raws))
	for i, raw := range raws {
		jobs[i].Raw = raw
		if err := json.Unmarshal(raw, &jobs[i].Job); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

// DownloadJobLog copies the log's bytes to w, following GitHub's redirect to blob storage.
func (h *HTTP) DownloadJobLog(ctx context.Context, jobID int64, w io.Writer) error {
	return h.get(ctx, h.repoURL+fmt.Sprintf("/actions/jobs/%d/logs", jobID), func(resp *http.Response) error {
		_, err := io.Copy(w, resp.Body)
		return err
	})
}

// list GETs a listing and every page its Link next URLs lead to, returning
// the elements of field and the total_count.
func (h *HTTP) list(ctx context.Context, path, field string) (elements []json.RawMessage, total int, err error) {
	for url := h.repoURL + path; url != ""; {
		var page map[string]json.RawMessage
		var items []json.RawMessage
		var next string
		err := h.get(ctx, url, func(resp *http.Response) error {
			next = nextLink(resp.Header.Get("Link"))
			if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
				return err
			}
			return errors.Join(json.Unmarshal(page["total_count"], &total), json.Unmarshal(page[field], &items))
		})
		if err != nil {
			return nil, 0, err
		}
		if next != "" {
			if u, err := neturl.Parse(next); err != nil || !h.onAPIHost(u) {
				return nil, 0, fmt.Errorf("%s: Link next %s is not on the API host", url, next)
			}
		}
		elements = append(elements, items...)
		url = next
	}
	return elements, total, nil
}

var linkNext = regexp.MustCompile(`<([^>]*)>;\s*rel="next"`)

func nextLink(header string) string {
	if m := linkNext.FindStringSubmatch(header); m != nil {
		return m[1]
	}
	return ""
}

func (h *HTTP) onAPIHost(u *neturl.URL) bool {
	return u.Host != "" && u.Scheme == h.api.Scheme && u.Host == h.api.Host
}

func (h *HTTP) getJSON(ctx context.Context, url string, v any) error {
	return h.get(ctx, url, func(resp *http.Response) error {
		return json.NewDecoder(resp.Body).Decode(v)
	})
}

func (h *HTTP) get(ctx context.Context, url string, read func(*http.Response) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "lg/"+version.Version)
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	if err := read(resp); err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	return nil
}
