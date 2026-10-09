package fakegithub

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// filteredCap is the most results GitHub returns for a filtered run listing.
const filteredCap = 1000

// serveRuns lists the loaded and added runs newest first, filtered by
// created and status.
func (s *Server) serveRuns(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	created := createdRange{to: maxTime}
	if query.Has("created") {
		var err error
		if created, err = parseCreated(query.Get("created")); err != nil {
			writeError(w, http.StatusUnprocessableEntity)
			return
		}
	}
	runs, err := s.listedRuns()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status := query.Get("status")
	var matched []json.RawMessage
	for _, run := range runs {
		if created.Contains(run.CreatedAt) && (status == "" || status == run.Status || status == run.Conclusion) {
			matched = append(matched, run.body)
		}
	}
	total := len(matched)
	if (query.Has("created") || query.Has("status")) && total > filteredCap {
		matched = matched[:filteredCap]
	}
	s.servePage(w, r, "workflow_runs", total, matched)
}

type listedRun struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	body       json.RawMessage
}

func (s *Server) listedRuns() ([]listedRun, error) {
	s.mu.Lock()
	bodies := slices.Clone(s.added)
	s.mu.Unlock()
	for _, run := range s.loaded() {
		body, err := served(run.files, "run.json")
		if err != nil {
			return nil, err
		}
		bodies = append(bodies, body)
	}
	runs := make([]listedRun, len(bodies))
	for i, body := range bodies {
		if err := json.Unmarshal(body, &runs[i]); err != nil {
			return nil, err
		}
		runs[i].body = body
	}
	slices.SortFunc(runs, func(a, b listedRun) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.ID, a.ID))
	})
	return runs, nil
}

// servePage serves the page of elements that per_page, page and the page
// cap select, with Links to the other pages. It wraps them in an object with
// total_count, or sends a bare array when field is "".
func (s *Server) servePage(w http.ResponseWriter, r *http.Request, field string, total int, elements []json.RawMessage) {
	size := min(intParam(r, "per_page", 30), 100)
	s.mu.Lock()
	if s.pageCap > 0 {
		size = min(size, s.pageCap)
	}
	s.mu.Unlock()
	page := intParam(r, "page", 1)
	last := max(1, (len(elements)+size-1)/size)
	from := min((page-1)*size, len(elements))
	to := min(from+size, len(elements))

	if link := linkHeader(repositoryURL(r), page, last); link != "" {
		w.Header().Set("Link", link)
	}
	var array bytes.Buffer
	array.WriteByte('[')
	for i, e := range elements[from:to] {
		if i > 0 {
			array.WriteByte(',')
		}
		array.Write(e)
	}
	array.WriteByte(']')
	if field == "" {
		s.writeJSON(w, r, array.Bytes())
		return
	}
	s.writeJSON(w, r, fmt.Appendf(nil, `{"total_count":%d,%q:%s}`, total, field, array.Bytes()))
}

func intParam(r *http.Request, name string, fallback int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

// repositoryURL is the request's URL with the repo addressed by id, as
// GitHub writes Link URLs.
func repositoryURL(r *http.Request) *url.URL {
	mount := ""
	if strings.HasPrefix(r.URL.Path, "/api/v3/") {
		mount = "/api/v3"
	}
	repo := "/repos/" + r.PathValue("owner") + "/" + r.PathValue("repo")
	if r.PathValue("repoid") != "" {
		repo = "/repositories/" + r.PathValue("repoid")
	}
	route := strings.TrimPrefix(r.URL.Path, mount+repo)
	return &url.URL{
		Scheme:   "http",
		Host:     r.Host,
		Path:     mount + "/repositories/" + repoID + route,
		RawQuery: r.URL.RawQuery,
	}
}

// linkHeader links to the other pages in GitHub's order: prev, next, last, first.
func linkHeader(u *url.URL, page, last int) string {
	at := func(n int, rel string) string {
		query := u.Query()
		query.Set("page", strconv.Itoa(n))
		paged := *u
		paged.RawQuery = query.Encode()
		return fmt.Sprintf(`<%s>; rel=%q`, paged.String(), rel)
	}
	var links []string
	if page > 1 {
		links = append(links, at(page-1, "prev"))
	}
	if page < last {
		links = append(links, at(page+1, "next"), at(last, "last"))
	}
	if page > 1 {
		links = append(links, at(1, "first"))
	}
	return strings.Join(links, ", ")
}

var maxTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// createdRange holds the times from and to, both included.
type createdRange struct{ from, to time.Time }

func (c createdRange) Contains(t time.Time) bool {
	return !t.Before(c.from) && !t.After(c.to)
}

func parseCreated(filter string) (createdRange, error) {
	if after, ok := strings.CutPrefix(filter, ">="); ok {
		from, _, err := createdBound(after)
		return createdRange{from: from, to: maxTime}, err
	}
	if a, b, ok := strings.Cut(filter, ".."); ok {
		from, _, errA := createdBound(a)
		_, to, errB := createdBound(b)
		return createdRange{from: from, to: to}, cmp.Or(errA, errB)
	}
	from, to, err := createdBound(filter)
	return createdRange{from: from, to: to}, err
}

// createdBound gives the first and last instants of a date, or a timestamp twice.
func createdBound(s string) (first, last time.Time, err error) {
	if day, err := time.Parse(time.DateOnly, s); err == nil {
		return day, day.Add(24*time.Hour - time.Nanosecond), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	return t, t, err
}
