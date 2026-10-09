// Package github reads GitHub Actions data through the REST API.
package github

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/version"
)

// Run is a run with the body GitHub served for it.
type Run struct {
	model.Run
	Raw json.RawMessage
}

// Artifact is an artifact with the element GitHub served for it.
type Artifact struct {
	model.Artifact
	Raw json.RawMessage
}

func (r *Run) UnmarshalJSON(raw []byte) error {
	r.Raw = slices.Clone(raw)
	return json.Unmarshal(raw, &r.Run)
}

func (r Run) MarshalJSON() ([]byte, error) { return r.Raw, nil }

func (a *Artifact) UnmarshalJSON(raw []byte) error {
	a.Raw = slices.Clone(raw)
	return json.Unmarshal(raw, &a.Artifact)
}

func (a Artifact) MarshalJSON() ([]byte, error) { return a.Raw, nil }

// Job is a job with the element GitHub served for it.
type Job struct {
	model.Job
	Raw json.RawMessage
}

type Client interface {
	GetRepo(ctx context.Context) (Repo, error)
	ListRuns(ctx context.Context, q RunQuery) (RunListing, error)
	GetRun(ctx context.Context, runID int64) (Run, error)
	GetAttempt(ctx context.Context, runID int64, attempt int) (Run, Source, error)
	ListAttemptJobs(ctx context.Context, runID int64, attempt int) ([]Job, Source, error)
	DownloadJobLog(ctx context.Context, jobID int64, w io.Writer) error
	JobLogURL(jobID int64) string
	ListArtifacts(ctx context.Context, runID int64) ([]Artifact, Source, error)
	DownloadArtifact(ctx context.Context, artifactID int64, w io.Writer) error
	ArtifactZipURL(artifactID int64) string
	CommitPulls(ctx context.Context, sha string) ([]CommitPull, Source, error)
}

// BaseURL is the REST API root for host: api.github.com for github.com and
// /api/v3 on any other host. A non-empty apiURL overrides both.
func BaseURL(host, apiURL string) (*url.URL, error) {
	switch {
	case apiURL != "":
		return url.Parse(strings.TrimRight(apiURL, "/"))
	case host == "github.com":
		return url.Parse("https://api.github.com")
	default:
		return url.Parse("https://" + host + "/api/v3")
	}
}

// Timeouts bound each wait on GitHub. No deadline bounds a whole request,
// so a long download finishes as long as its bytes keep coming.
type Timeouts struct {
	Dial, TLSHandshake, ResponseHeader, BodyIdle time.Duration
}

func DefaultTimeouts() Timeouts {
	return Timeouts{Dial: 10 * time.Second, TLSHandshake: 10 * time.Second, ResponseHeader: 30 * time.Second, BodyIdle: time.Minute}
}

func NewTransport(t Timeouts) http.RoundTripper {
	dialer := &net.Dialer{Timeout: t.Dial}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = dialer.DialContext
	base.TLSHandshakeTimeout = t.TLSHandshake
	base.ResponseHeaderTimeout = t.ResponseHeader
	base.OnProxyConnectResponse = func(_ context.Context, proxy *url.URL, req *http.Request, resp *http.Response) error {
		if resp.StatusCode/100 != 2 {
			return &proxyConnectError{proxy: proxy.Host, target: req.Host, status: resp.Status}
		}
		return nil
	}
	return &idleTransport{base: base, timeouts: t}
}

// proxyConnectError is a proxy's refusal to tunnel to a host.
type proxyConnectError struct{ proxy, target, status string }

func (e *proxyConnectError) Error() string {
	return fmt.Sprintf("proxy %s answered CONNECT %s with %s", e.proxy, e.target, e.status)
}

// idleTransport cancels a request once its body has sent nothing for BodyIdle.
type idleTransport struct {
	base     *http.Transport
	timeouts Timeouts
}

func (t *idleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &idleBody{ReadCloser: resp.Body, cancel: cancel, idle: t.timeouts.BodyIdle}
	return resp, nil
}

type idleBody struct {
	io.ReadCloser
	cancel  context.CancelFunc
	idle    time.Duration
	stalled atomic.Bool
}

func (b *idleBody) Read(p []byte) (int, error) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-clock.Real{}.After(b.idle):
			b.stalled.Store(true)
			b.cancel()
		case <-done:
		}
	}()
	n, err := b.ReadCloser.Read(p)
	if err != nil && b.stalled.Load() {
		err = fmt.Errorf("body idle for %s: %w", b.idle, err)
	}
	return n, err
}

func (b *idleBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

// A failed request says which hop failed: the API, or the blob storage it redirects to.
var (
	ErrNotFound    = errors.New("not found")
	ErrBlobMissing = errors.New("blob missing")
	ErrGone        = errors.New("gone")
	// ErrUnauthorized marks a Blocked caused by the API's 401.
	ErrUnauthorized = errors.New("unauthorized")
)

type unauthorized struct{ failure.Blocked }

func (u unauthorized) Unwrap() []error { return []error{u.Blocked, ErrUnauthorized} }

// StatusError is a response other than 200 to a request for URL, an API
// URL even when blob storage answered.
type StatusError struct {
	URL     string
	Status  int
	Message string
	Blob    bool
}

func (e *StatusError) Error() string {
	hop := ""
	if e.Blob {
		hop = "blob storage answered "
	}
	return fmt.Sprintf("%s: %s%d %s", e.URL, hop, e.Status, http.StatusText(e.Status))
}

func (e *StatusError) Unwrap() error {
	switch {
	case e.Status == http.StatusGone:
		return ErrGone
	case e.Status == http.StatusNotFound && e.Blob:
		return ErrBlobMissing
	case e.Status == http.StatusNotFound:
		return ErrNotFound
	}
	return nil
}

// MalformedError is a 200 whose body lg cannot use.
type MalformedError struct{ Err error }

func (e *MalformedError) Error() string { return e.Err.Error() }

func (e *MalformedError) Unwrap() error { return e.Err }

func malformed(rawURL, format string, args ...any) error {
	return fmt.Errorf("%s: %w", rawURL, &MalformedError{Err: fmt.Errorf(format, args...)})
}

// Source is where a file came from: an API URL, never a blob URL.
type Source struct {
	URL   string
	Pages int
}

// HTTP sends requests straight to a transport, so that only lg follows
// redirects and no error names a blob URL.
type HTTP struct {
	transport http.RoundTripper
	clock     clock.Clock
	api       url.URL
	repoURL   string
	token     string
	cache     *Cache

	mu sync.Mutex
	// rateLimit holds the last API response's header, received at rateLimitAt.
	rateLimit   http.Header
	rateLimitAt time.Time
}

func NewHTTP(transport http.RoundTripper, api *url.URL, repo, token string, clk clock.Clock) *HTTP {
	return &HTTP{transport: transport, clock: clk, api: *api, repoURL: api.String() + "/repos/" + repo, token: token}
}

// NewDefault is the Client lg sync uses, with DefaultTimeouts.
func NewDefault(api *url.URL, repo, token string, cache *Cache, clk clock.Clock) Client {
	return NewHTTP(NewTransport(DefaultTimeouts()), api, repo, token, clk).WithCache(cache)
}

// Repo is a repository as GET /repos/{owner}/{repo} describes it.
type Repo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
}

func (h *HTTP) GetRepo(ctx context.Context) (Repo, error) {
	var repo Repo
	if err := h.getJSON(ctx, h.revalidate, h.repoURL, &repo); err != nil {
		return Repo{}, err
	}
	if !layout.IsRepo(repo.FullName) {
		return Repo{}, malformed(h.repoURL, "full_name %q is not owner/name", repo.FullName)
	}
	return repo, nil
}

// RunQuery filters a run listing: the runs created in [From, To], when
// set, with Status, when set.
type RunQuery struct {
	From, To time.Time
	Status   string
	PerPage  int
	Page     int
}

// Values encodes the query as GitHub reads it, leaving out what is unset.
func (q RunQuery) Values() url.Values {
	v := url.Values{}
	if !q.From.IsZero() || !q.To.IsZero() {
		v.Set("created", q.From.UTC().Format(time.RFC3339)+".."+q.To.UTC().Format(time.RFC3339))
	}
	if q.Status != "" {
		v.Set("status", q.Status)
	}
	if q.PerPage > 0 {
		v.Set("per_page", strconv.Itoa(q.PerPage))
	}
	if q.Page > 0 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	return v
}

// Narrowable reports whether a capped listing of q can be narrowed: by a
// created range when it has none, and by halving one whose bounds are
// different seconds, since GitHub filters created at whole seconds.
func (q RunQuery) Narrowable() bool {
	return q.From.IsZero() && q.To.IsZero() || q.To.After(q.From)
}

// ListingCap is the most results GitHub serves for a filtered run listing.
const ListingCap = 1000

// RunListing is a run listing: its runs newest first and GitHub's total_count.
// Capped says GitHub serves fewer runs than Total: Total reaches ListingCap,
// and ListRuns stopped at the first page of a Narrowable query or GitHub
// stopped at ListingCap runs.
type RunListing struct {
	Runs   []Run
	Total  int
	Capped bool
}

// ListRuns lists the runs q selects. PerPage defaults to 100. A listing
// whose total_count reaches ListingCap stops after its first page when q is
// Narrowable, since GitHub serves no more of it and the caller narrows q;
// otherwise it pages through what GitHub serves. It revalidates a listing
// without a created range, since lg's ranges move with the clock.
func (h *HTTP) ListRuns(ctx context.Context, q RunQuery) (RunListing, error) {
	if q.PerPage == 0 {
		q.PerPage = 100
	}
	limit := 0
	if q.Narrowable() {
		limit = ListingCap
	}
	get := h.revalidate
	if !q.From.IsZero() || !q.To.IsZero() {
		get = h.get
	}
	l, err := h.list(ctx, get, h.repoURL+"/actions/runs?"+q.Values().Encode(), "workflow_runs", limit)
	if err != nil {
		return RunListing{}, err
	}
	runs := make([]Run, len(l.elements))
	for i, raw := range l.elements {
		if err := json.Unmarshal(raw, &runs[i]); err != nil {
			return RunListing{}, err
		}
	}
	capped := l.total >= ListingCap && (l.more || len(runs) >= ListingCap)
	return RunListing{Runs: runs, Total: l.total, Capped: capped}, nil
}

func (h *HTTP) GetRun(ctx context.Context, runID int64) (Run, error) {
	var run Run
	err := h.getJSON(ctx, h.revalidate, h.repoURL+fmt.Sprintf("/actions/runs/%d", runID), &run)
	return run, err
}

func (h *HTTP) GetAttempt(ctx context.Context, runID int64, attempt int) (Run, Source, error) {
	source := Source{URL: h.repoURL + fmt.Sprintf("/actions/runs/%d/attempts/%d", runID, attempt)}
	var run Run
	if err := h.getJSON(ctx, h.get, source.URL, &run.Raw); err != nil {
		return Run{}, Source{}, err
	}
	if err := json.Unmarshal(run.Raw, &run.Run); err != nil {
		return Run{}, Source{}, malformed(source.URL, "%w", err)
	}
	switch {
	case run.Status == "":
		return Run{}, Source{}, malformed(source.URL, "no status")
	case run.UpdatedAt.IsZero():
		return Run{}, Source{}, malformed(source.URL, "no updated_at")
	case run.RunStartedAt.IsZero():
		return Run{}, Source{}, malformed(source.URL, "no run_started_at")
	case run.RunAttempt != attempt:
		return Run{}, Source{}, malformed(source.URL, "run_attempt is %d", run.RunAttempt)
	}
	return run, source, nil
}

func (h *HTTP) ListAttemptJobs(ctx context.Context, runID int64, attempt int) ([]Job, Source, error) {
	listURL := h.repoURL + fmt.Sprintf("/actions/runs/%d/attempts/%d/jobs?per_page=100", runID, attempt)
	return listByID(ctx, h, h.get, listURL, "jobs", "job", func(raw json.RawMessage) (Job, int64, error) {
		job := Job{Raw: raw}
		err := json.Unmarshal(raw, &job.Job)
		return job, job.ID, err
	})
}

// DownloadJobLog copies the log's bytes to w, following GitHub's redirect to blob storage.
func (h *HTTP) DownloadJobLog(ctx context.Context, jobID int64, w io.Writer) error {
	return h.download(ctx, h.JobLogURL(jobID), w)
}

func (h *HTTP) download(ctx context.Context, rawURL string, w io.Writer) error {
	return h.get(ctx, rawURL, func(resp *http.Response) error {
		_, err := io.Copy(w, resp.Body)
		return err
	})
}

func (h *HTTP) JobLogURL(jobID int64) string {
	return h.repoURL + fmt.Sprintf("/actions/jobs/%d/logs", jobID)
}

func (h *HTTP) ListArtifacts(ctx context.Context, runID int64) ([]Artifact, Source, error) {
	listURL := h.repoURL + fmt.Sprintf("/actions/runs/%d/artifacts?per_page=100", runID)
	artifacts, source, err := listByID(ctx, h, h.revalidate, listURL, "artifacts", "artifact", func(raw json.RawMessage) (Artifact, int64, error) {
		var artifact Artifact
		err := json.Unmarshal(raw, &artifact)
		return artifact, artifact.ID, err
	})
	if err != nil {
		return nil, Source{}, err
	}
	for _, artifact := range artifacts {
		if artifact.CreatedAt.IsZero() {
			return nil, Source{}, malformed(listURL, "artifact %d has no created_at", artifact.ID)
		}
	}
	return artifacts, source, nil
}

// listByID lists the elements of field, which decode reads with their ids.
// It refuses a listing short of its total_count or one that repeats an id.
func listByID[T any](ctx context.Context, h *HTTP, get getter, listURL, field, noun string, decode func(json.RawMessage) (T, int64, error)) ([]T, Source, error) {
	l, err := h.list(ctx, get, listURL, field, 0)
	if err != nil {
		return nil, Source{}, err
	}
	if l.total > len(l.elements) {
		return nil, Source{}, malformed(listURL, "listed %d of %d %s", len(l.elements), l.total, field)
	}
	elements := make([]T, len(l.elements))
	listed := map[int64]bool{}
	for i, raw := range l.elements {
		element, id, err := decode(raw)
		if err != nil {
			return nil, Source{}, malformed(listURL, "%w", err)
		}
		if id <= 0 {
			return nil, Source{}, malformed(listURL, "%s #%d has no id", noun, i)
		}
		if listed[id] {
			return nil, Source{}, malformed(listURL, "%s %d listed twice", noun, id)
		}
		listed[id] = true
		elements[i] = element
	}
	return elements, Source{URL: listURL, Pages: l.pages}, nil
}

// DownloadArtifact copies the artifact's zip to w, following GitHub's redirect to blob storage.
func (h *HTTP) DownloadArtifact(ctx context.Context, artifactID int64, w io.Writer) error {
	return h.download(ctx, h.ArtifactZipURL(artifactID), w)
}

func (h *HTTP) ArtifactZipURL(artifactID int64) string {
	return h.repoURL + fmt.Sprintf("/actions/artifacts/%d/zip", artifactID)
}

// listing is what list read: the elements of its field over pages pages,
// the total_count, and whether it left a Link next unread.
type listing struct {
	elements     []json.RawMessage
	total, pages int
	more         bool
}

// list GETs a listing and every page its Link next URLs lead to. It stops
// after a page whose total_count reaches limit, when limit is set.
func (h *HTTP) list(ctx context.Context, get getter, firstURL, field string, limit int) (listing, error) {
	var l listing
	var err error
	l.pages, l.more, err = h.paginate(ctx, get, firstURL, func(resp *http.Response) (bool, error) {
		var page map[string]json.RawMessage
		var items []json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			return false, &MalformedError{Err: err}
		}
		if err := unmarshalField(page, "total_count", &l.total); err != nil {
			return false, err
		}
		if err := unmarshalField(page, field, &items); err != nil {
			return false, err
		}
		l.elements = append(l.elements, items...)
		return limit > 0 && l.total >= limit, nil
	})
	if err != nil {
		return listing{}, err
	}
	return l, nil
}

// paginate GETs firstURL and every page its Link next URLs lead to, reading
// each with read until read says to stop. It gives how many pages it read,
// and whether it left a Link next unread.
func (h *HTTP) paginate(ctx context.Context, get getter, firstURL string, read func(*http.Response) (stop bool, err error)) (pages int, more bool, err error) {
	followed := map[string]bool{}
	for pageURL := firstURL; pageURL != ""; {
		followed[pageURL] = true
		var next string
		var stop bool
		err := get(ctx, pageURL, func(resp *http.Response) error {
			next = nextLink(resp.Header.Get("Link"))
			var err error
			stop, err = read(resp)
			return err
		})
		if err != nil {
			return 0, false, err
		}
		if next != "" {
			if u, err := url.Parse(next); err != nil || !h.onAPIHost(u) {
				return 0, false, malformed(pageURL, "Link next %s is not on the API host", next)
			}
			if followed[next] {
				return 0, false, malformed(pageURL, "Link next %s repeats an earlier page", next)
			}
		}
		if stop {
			return len(followed), next != "", nil
		}
		pageURL = next
	}
	return len(followed), false, nil
}

func unmarshalField(object map[string]json.RawMessage, field string, v any) error {
	raw, ok := object[field]
	if !ok {
		return &MalformedError{Err: fmt.Errorf("no %q", field)}
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return &MalformedError{Err: fmt.Errorf("%q: %w", field, err)}
	}
	return nil
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

func (h *HTTP) getJSON(ctx context.Context, get getter, rawURL string, v any) error {
	return get(ctx, rawURL, func(resp *http.Response) error {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			return &MalformedError{Err: err}
		}
		return nil
	})
}

// getter GETs rawURL and reads the answer.
type getter func(ctx context.Context, rawURL string, read func(*http.Response) error) error

func (h *HTTP) get(ctx context.Context, rawURL string, read func(*http.Response) error) error {
	return h.getIfNoneMatch(ctx, rawURL, "", read)
}

// getIfNoneMatch GETs rawURL, sending etag as If-None-Match when it is set,
// and reads a 200, or a 304 to etag.
func (h *HTTP) getIfNoneMatch(ctx context.Context, rawURL, etag string, read func(*http.Response) error) error {
	h.mu.Lock()
	blocked, reserved := failure.Reserve(h.rateLimit, h.rateLimitAt, h.clock.Now())
	h.mu.Unlock()
	if reserved {
		return blocked
	}
	resp, err := h.follow(ctx, rawURL, etag)
	if err != nil {
		err = fmt.Errorf("%s: %w", rawURL, err)
		var opErr *net.OpError
		var connectErr *proxyConnectError
		switch {
		case ctx.Err() != nil:
			return err
		case errors.As(err, &opErr) && (opErr.Op == "dial" || opErr.Op == "proxyconnect"), errors.As(err, &connectErr):
			return failure.Blocked{Kind: failure.Unreachable, Detail: err.Error()}
		}
		return failure.Transient{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && (etag == "" || resp.StatusCode != http.StatusNotModified) {
		return h.statusError(rawURL, resp)
	}
	body := &readErrors{ReadCloser: resp.Body}
	resp.Body = body
	if err := read(resp); err != nil {
		err = fmt.Errorf("%s: %w", rawURL, err)
		if body.err != nil && ctx.Err() == nil {
			return failure.Transient{Err: err}
		}
		return err
	}
	return nil
}

// follow GETs rawURL and follows its redirects. Its errors never name a
// redirect target, since a blob URL's query is a credential.
func (h *HTTP) follow(ctx context.Context, rawURL, etag string) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	for range maxRequests {
		resp, err := h.do(ctx, u, etag)
		if err != nil {
			return nil, err
		}
		location := resp.Header.Get("Location")
		if !isRedirect(resp.StatusCode) || location == "" {
			return resp, nil
		}
		_ = resp.Body.Close()
		if u, err = u.Parse(location); err != nil {
			return nil, errors.New("unparsable redirect Location")
		}
	}
	return nil, fmt.Errorf("stopped after %d redirects", maxRequests)
}

const maxRequests = 10

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// UserAgent names lg and its version to GitHub.
func UserAgent() string { return "lg/" + version.Version }

// do sends the token only to the API host. Go's own rule would send it to a
// blob host that differs only by port or is a subdomain.
func (h *HTTP) do(ctx context.Context, u *url.URL, etag string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", UserAgent())
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if !h.onAPIHost(u) {
		return h.transport.RoundTrip(req)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := h.transport.RoundTrip(req)
	if err == nil {
		h.mu.Lock()
		h.rateLimit, h.rateLimitAt = resp.Header, h.clock.Now()
		h.mu.Unlock()
	}
	return resp, err
}

// statusError classifies a failed response by the hop that sent it.
func (h *HTTP) statusError(rawURL string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	e := &StatusError{URL: rawURL, Status: resp.StatusCode, Message: message(body, resp.StatusCode), Blob: !h.onAPIHost(resp.Request.URL)}
	blocked, refused := failure.FromStatus(e.Status, resp.Header, e.Message, e.Error()+": "+e.Message, h.clock.Now())
	switch {
	case refused && !e.Blob && e.Status == http.StatusUnauthorized:
		return unauthorized{blocked}
	case refused && !e.Blob:
		return blocked
	case refused, e.Status >= 500:
		// lg sends blob storage no token, so its refusals are Transient.
		return failure.Transient{Err: e}
	}
	return e
}

// message is GitHub's JSON message, or the first line of blob storage's XML
// Message, or else the status text.
func message(body []byte, status int) string {
	var m struct{ Message string }
	if json.Unmarshal(body, &m) == nil && m.Message != "" {
		return m.Message
	}
	if xml.Unmarshal(body, &m) == nil && m.Message != "" {
		first, _, _ := strings.Cut(m.Message, "\n")
		return first
	}
	return http.StatusText(status)
}

// readErrors remembers a failed read, which leaves a body short.
type readErrors struct {
	io.ReadCloser
	err error
}

func (r *readErrors) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

// CommitPull is a pull request that holds a commit. HeadRepoID is 0 when
// GitHub gives no head repo, as for a deleted fork.
type CommitPull struct {
	Number     int
	HeadRef    string
	HeadRepoID int64
}

type pullJSON struct {
	Number int `json:"number"`
	Head   struct {
		Ref  string `json:"ref"`
		Repo *struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"head"`
}

func (p *CommitPull) UnmarshalJSON(raw []byte) error {
	var pr pullJSON
	if err := json.Unmarshal(raw, &pr); err != nil {
		return err
	}
	*p = CommitPull{Number: pr.Number, HeadRef: pr.Head.Ref}
	if pr.Head.Repo != nil {
		p.HeadRepoID = pr.Head.Repo.ID
	}
	return nil
}

// ErrUnknownCommit is GitHub's 422 to the first page of a commit's pulls.
var ErrUnknownCommit = errors.New("unknown commit")

// CommitPulls lists the pull requests that hold the commit sha. It gives
// ErrUnknownCommit, with the listing's source, for a commit GitHub does not know.
func (h *HTTP) CommitPulls(ctx context.Context, sha string) ([]CommitPull, Source, error) {
	listURL := h.repoURL + "/commits/" + url.PathEscape(sha) + "/pulls?per_page=100"
	var pulls []CommitPull
	read := 0
	pages, _, err := h.paginate(ctx, h.get, listURL, func(resp *http.Response) (bool, error) {
		read++
		var page []json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			return false, &MalformedError{Err: err}
		}
		for _, raw := range page {
			var pull CommitPull
			if err := json.Unmarshal(raw, &pull); err != nil {
				return false, &MalformedError{Err: fmt.Errorf("pull %d: %w", len(pulls), err)}
			}
			pulls = append(pulls, pull)
		}
		return false, nil
	})
	var statusErr *StatusError
	if read == 0 && errors.As(err, &statusErr) && statusErr.Status == http.StatusUnprocessableEntity {
		return nil, Source{URL: listURL, Pages: 1}, ErrUnknownCommit
	}
	if err != nil {
		return nil, Source{}, err
	}
	return pulls, Source{URL: listURL, Pages: pages}, nil
}
