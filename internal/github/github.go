// Package github reads GitHub Actions data through the REST API.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

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
func BaseURL(host, apiURL string) (*url.URL, error) {
	switch {
	case apiURL != "":
		return url.Parse(apiURL)
	case host == "github.com":
		return url.Parse("https://api.github.com")
	default:
		return url.Parse("https://" + host + "/api/v3")
	}
}

// Timeouts bound each wait on GitHub. No deadline bounds a whole request.
type Timeouts struct {
	Dial, TLSHandshake, ResponseHeader, BodyIdle time.Duration
}

func NewHTTPClient(Timeouts) *http.Client { return &http.Client{} }

type HTTP struct {
	client  *http.Client
	api     url.URL
	repoURL string
	token   string
}

func NewHTTP(client *http.Client, api *url.URL, repo, token string) *HTTP {
	h := &HTTP{api: *api, repoURL: api.String() + "/repos/" + repo, token: token}
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
func (h *HTTP) list(ctx context.Context, path, field string) ([]json.RawMessage, int, error) {
	var elements []json.RawMessage
	var total int
	followed := map[string]bool{}
	for pageURL := h.repoURL + path; pageURL != ""; {
		followed[pageURL] = true
		var page map[string]json.RawMessage
		var items []json.RawMessage
		var next string
		err := h.get(ctx, pageURL, func(resp *http.Response) error {
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
			if u, err := url.Parse(next); err != nil || !h.onAPIHost(u) {
				return nil, 0, fmt.Errorf("%s: Link next %s is not on the API host", pageURL, next)
			}
			if followed[next] {
				return nil, 0, fmt.Errorf("%s: Link next %s repeats an earlier page", pageURL, next)
			}
		}
		elements = append(elements, items...)
		pageURL = next
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

func (h *HTTP) onAPIHost(u *url.URL) bool {
	return u.Scheme == h.api.Scheme && strings.EqualFold(u.Hostname(), h.api.Hostname()) && port(u) == port(&h.api)
}

func port(u *url.URL) string {
	switch {
	case u.Port() != "":
		return u.Port()
	case u.Scheme == "https":
		return "443"
	default:
		return "80"
	}
}

func (h *HTTP) getJSON(ctx context.Context, rawURL string, v any) error {
	return h.get(ctx, rawURL, func(resp *http.Response) error {
		return json.NewDecoder(resp.Body).Decode(v)
	})
}

func (h *HTTP) get(ctx context.Context, rawURL string, read func(*http.Response) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
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
		return fmt.Errorf("%s: %s", rawURL, resp.Status)
	}
	if err := read(resp); err != nil {
		return fmt.Errorf("%s: %w", rawURL, err)
	}
	return nil
}
