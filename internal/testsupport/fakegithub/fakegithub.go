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
}

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

func (s *Server) URL() string { return s.api.URL }

func (s *Server) Close() {
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
		s.mu.Unlock()
		h.ServeHTTP(w, r)
	})
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
