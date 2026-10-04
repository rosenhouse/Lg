// Package fakegithub serves recorded GitHub Actions API responses.
package fakegithub

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

type Request struct {
	Host   string // "api" or "blob"
	Method string
	Path   string
	Query  string
	Status int

	Authorization bool
}

// Server is an API host on 127.0.0.1 that redirects downloads to a blob host
// addressed as localhost, as GitHub redirects them to another domain.
type Server struct {
	api, blob *httptest.Server

	mu       sync.Mutex
	runs     map[string]*run
	first    string
	added    []json.RawMessage
	pageCap  int
	token    string
	requests []Request
	faults   []*fault
	holds    []*hold
}

// run is a recorded run at one stage.
type run struct {
	dir string
	// downloads holds each status.txt line by its path, and blobs by the
	// file record.sh wrote its body to.
	downloads map[string]download
	blobs     map[string]download
}

type download struct {
	recordings.Line
	file string
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
	s := &Server{runs: map[string]*run{}}
	s.api = httptest.NewServer(s.record("api", s.routes()))
	s.blob = httptest.NewServer(s.record("blob", http.HandlerFunc(s.serveBlob)))
	return s
}

// Load serves a run at a stage, alongside any runs already loaded.
func (s *Server) Load(runID int64, stage string) error {
	return s.LoadDir(runID, Recording(runID, stage))
}

// LoadDir serves the recording in dir as run runID.
func (s *Server) LoadDir(runID int64, dir string) error {
	status, err := os.Open(filepath.Join(dir, "status.txt"))
	if err != nil {
		return fmt.Errorf("no recording of run %d: %w", runID, err)
	}
	defer func() { _ = status.Close() }()
	lines, err := recordings.ParseStatus(status)
	if err != nil {
		return fmt.Errorf("%s: %w", status.Name(), err)
	}
	r := &run{dir: dir, downloads: map[string]download{}, blobs: map[string]download{}}
	for _, line := range lines {
		d := download{Line: line, file: downloadFile(dir, line.Path)}
		r.downloads[line.Path] = d
		if d.file != "" {
			r.blobs[d.file] = d
		}
	}
	id := strconv.FormatInt(runID, 10)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.first == "" {
		s.first = id
	}
	s.runs[id] = r
	return nil
}

// Advance serves a loaded run at another stage.
func (s *Server) Advance(runID int64, stage string) error {
	s.mu.Lock()
	_, loaded := s.runs[strconv.FormatInt(runID, 10)]
	s.mu.Unlock()
	if !loaded {
		return fmt.Errorf("run %d is not loaded", runID)
	}
	return s.Load(runID, stage)
}

// downloadFile is the file, relative to dir, that record.sh wrote a
// download's body to, or "" for a JSON GET.
func downloadFile(dir, path string) string {
	parts := strings.Split(path, "/")
	switch {
	case len(parts) == 3 && parts[0] == "jobs" && parts[2] == "logs":
		matches, _ := filepath.Glob(filepath.Join(dir, "attempt-*", "logs", parts[1]+".txt"))
		if len(matches) == 1 {
			rel, _ := filepath.Rel(dir, matches[0])
			return rel
		}
	case len(parts) == 5 && parts[0] == "runs" && parts[2] == "attempts" && parts[4] == "logs":
		return filepath.Join("attempt-"+parts[3], "logs.zip")
	case len(parts) == 3 && parts[0] == "artifacts" && parts[2] == "zip":
		return filepath.Join("artifacts", parts[1]+".zip")
	}
	return ""
}

// Recording is the dir under testdata/recordings holding a run at a stage.
func Recording(runID int64, stage string) string {
	return filepath.Join(recordingsDir(), fmt.Sprintf("run-%d", runID), stage)
}

// Served is a file of the first run loaded, as the server serves it: JSON
// compacted, anything else as recorded.
func (s *Server) Served(elem ...string) []byte {
	ginkgo.GinkgoHelper()
	s.mu.Lock()
	dir := s.runs[s.first].dir
	s.mu.Unlock()
	body, err := served(filepath.Join(dir, filepath.Join(elem...)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return body
}

func served(file string) ([]byte, error) {
	recorded, err := os.ReadFile(file)
	if err != nil || !json.Valid(recorded) {
		return recorded, err
	}
	var compact bytes.Buffer
	err = json.Compact(&compact, recorded)
	return compact.Bytes(), err
}

func (s *Server) URL() string { return s.api.URL }

// SetPageCap pages every listing at most n elements per page.
func (s *Server) SetPageCap(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageCap = n
}

// RequireToken answers 401 to API requests without Authorization: Bearer token.
func (s *Server) RequireToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
}

// AddRun lists a run with the given body.
func (s *Server) AddRun(body json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.added = append(s.added, body)
}

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

// statusWriter remembers the status a handler wrote.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (s *Server) record(host string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		i := len(s.requests)
		s.requests = append(s.requests, Request{
			Host: host, Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
			Authorization: r.Header.Get("Authorization") != "",
		})
		holds := s.holdsOf(r.URL.Path)
		status := s.takeFault(host, r.URL.Path)
		if host == "api" && s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
			status = http.StatusUnauthorized
		}
		s.mu.Unlock()
		for _, held := range holds {
			select {
			case <-held.released:
			case <-r.Context().Done():
				return
			}
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		if status != 0 {
			writeError(sw, status)
		} else {
			h.ServeHTTP(sw, r)
		}
		s.mu.Lock()
		s.requests[i].Status = sw.status
		s.mu.Unlock()
	})
}

func writeError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"message":%q,"documentation_url":"https://docs.github.com/rest","status":"%d"}`, http.StatusText(status), status)
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

// recordingsDir finds testdata/recordings from this source file, so it works
// from any package's test binary.
func recordingsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "recordings")
}
