// Package fakegithub serves recorded GitHub Actions API responses.
package fakegithub

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

type Request struct {
	Host   string // "api" or "blob"
	Method string
	Path   string
	Query  string
	Status int

	Authorization bool
}

const recordedRepo = "rosenhouse/lg"

// Server is an API host on 127.0.0.1 that redirects downloads to a blob host
// addressed as localhost, as GitHub redirects them to another domain.
type Server struct {
	api, blob *httptest.Server

	mu       sync.Mutex
	runID    string
	runDir   string
	requests []Request
	faults   []*fault
	holds    []*hold
}

// Fault answers a request with Status. Times limits how many requests it
// answers; 0 means every one.
type Fault struct {
	Status int
	Times  int
}

type fault struct {
	Fault
	host, match string
	answered    int
}

type hold struct {
	match    string
	released chan struct{}
	once     sync.Once
}

func (h *hold) release() { h.once.Do(func() { close(h.released) }) }

// Start serves a run at a stage until the spec ends.
func Start(runID int64, stage string) *Server {
	ginkgo.GinkgoHelper()
	s := New()
	ginkgo.DeferCleanup(s.Close)
	gomega.Expect(s.Load(runID, stage)).To(gomega.Succeed())
	return s
}

func New() *Server {
	s := &Server{}
	s.api = httptest.NewServer(s.record("api", s.apiRoutes()))
	s.blob = httptest.NewServer(s.record("blob", http.HandlerFunc(s.serveBlob)))
	return s
}

func (s *Server) Load(runID int64, stage string) error {
	return s.LoadDir(runID, Recording(runID, stage))
}

// LoadDir serves the recording in dir as run runID.
func (s *Server) LoadDir(runID int64, dir string) error {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("no recording of run %d: %s", runID, dir)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runID = strconv.FormatInt(runID, 10)
	s.runDir = dir
	return nil
}

// Recording is the dir under testdata/recordings holding a run at a stage.
func Recording(runID int64, stage string) string {
	return filepath.Join(recordingsDir(), fmt.Sprintf("run-%d", runID), stage)
}

// Served is a recorded file's body as the server serves it: JSON compacted,
// anything else as recorded.
func (s *Server) Served(elem ...string) []byte {
	ginkgo.GinkgoHelper()
	body, err := s.served(filepath.Join(elem...))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return body
}

func (s *Server) served(name string) ([]byte, error) {
	recorded, err := os.ReadFile(filepath.Join(s.dir(), name))
	if err != nil || filepath.Ext(name) != ".json" {
		return recorded, err
	}
	var compact bytes.Buffer
	err = json.Compact(&compact, recorded)
	return compact.Bytes(), err
}

func (s *Server) dir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runDir
}

// Advance serves a loaded run at another stage.
func (s *Server) Advance(runID int64, stage string) error { return nil }

// SetPageCap pages every listing at most n elements per page.
func (s *Server) SetPageCap(n int) {}

// RequireToken answers 401 to API requests without Authorization: Bearer token.
func (s *Server) RequireToken(token string) {}

// AddRun lists a run with the given body.
func (s *Server) AddRun(run json.RawMessage) {}

func (s *Server) URL() string { return s.api.URL }

// Fail answers requests to host ("api" or "blob") whose path ends in match with f.
func (s *Server) Fail(host, match string, f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append(s.faults, &fault{Fault: f, host: host, match: match})
}

// Hold delays requests whose path ends in match until release is called.
func (s *Server) Hold(match string) (release func()) {
	h := &hold{match: match, released: make(chan struct{})}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holds = append(s.holds, h)
	return h.release
}

func (s *Server) Close() {
	s.mu.Lock()
	for _, h := range s.holds {
		h.release()
	}
	s.mu.Unlock()
	s.api.Close()
	s.blob.Close()
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) record(host string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, Request{Host: host, Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery})
		holds := s.holdsOf(r.URL.Path)
		status := s.takeFault(host, r.URL.Path)
		s.mu.Unlock()
		for _, held := range holds {
			select {
			case <-held.released:
			case <-r.Context().Done():
				return
			}
		}
		if status != 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"message":%q}`, http.StatusText(status))
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) holdsOf(path string) []*hold {
	var holds []*hold
	for _, h := range s.holds {
		if strings.HasSuffix(path, h.match) {
			holds = append(holds, h)
		}
	}
	return holds
}

// takeFault returns the status of the first matching fault with answers
// left, or 0, and counts this answer.
func (s *Server) takeFault(host, path string) int {
	for _, f := range s.faults {
		if f.host == host && strings.HasSuffix(path, f.match) && (f.Times == 0 || f.answered < f.Times) {
			f.answered++
			return f.Status
		}
	}
	return 0
}

func (s *Server) apiRoutes() http.Handler {
	mux := http.NewServeMux()
	const repo = "GET /repos/{owner}/{repo}/actions"
	mux.HandleFunc(repo+"/runs", ofRepo(func(w http.ResponseWriter, r *http.Request) {
		s.serveJSON(w, r, `{"total_count":1,"workflow_runs":[%s]}`, "run.json")
	}))
	mux.HandleFunc(repo+"/runs/{id}/attempts/{n}", s.ofRun(func(w http.ResponseWriter, r *http.Request) {
		s.serveJSON(w, r, "%s", "attempt-"+r.PathValue("n"), "attempt.json")
	}))
	mux.HandleFunc(repo+"/runs/{id}/attempts/{n}/jobs", s.ofRun(func(w http.ResponseWriter, r *http.Request) {
		s.serveJSON(w, r, "%s", "attempt-"+r.PathValue("n"), "jobs.json")
	}))
	mux.HandleFunc(repo+"/jobs/{id}/logs", ofRepo(func(w http.ResponseWriter, r *http.Request) {
		blobURL := strings.Replace(s.blob.URL, "127.0.0.1", "localhost", 1) + "/logs/" + r.PathValue("id")
		http.Redirect(w, r, blobURL, http.StatusFound)
	}))
	return mux
}

// ofRepo answers 404 for any repo but the recorded one, which GitHub matches case-insensitively.
func ofRepo(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.PathValue("owner")+"/"+r.PathValue("repo"), recordedRepo) {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}

// ofRun also answers 404 for any run but the loaded one.
func (s *Server) ofRun(h http.HandlerFunc) http.HandlerFunc {
	return ofRepo(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		runID := s.runID
		s.mu.Unlock()
		if r.PathValue("id") != runID {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	})
}

func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/logs/")
	matches, _ := filepath.Glob(filepath.Join(s.dir(), "attempt-*", "logs", id+".txt"))
	if len(matches) == 0 {
		http.NotFound(w, r)
		return
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(body)
}

// serveJSON serves a recorded file as served, inside wrapper.
func (s *Server) serveJSON(w http.ResponseWriter, r *http.Request, wrapper string, elem ...string) {
	body, err := s.served(filepath.Join(elem...))
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = fmt.Fprintf(w, wrapper, body)
}

// recordingsDir finds testdata/recordings from this source file, so it works
// from any package's test binary.
func recordingsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "recordings")
}
