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
	"strings"
	"sync"
)

type Request struct {
	Host   string // "api" or "blob"
	Method string
	Path   string
	Query  string
}

// Server is an API host on 127.0.0.1 that redirects downloads to a blob host
// addressed as localhost, as GitHub redirects them to another domain.
type Server struct {
	api, blob *httptest.Server
	runDir    string

	mu       sync.Mutex
	requests []Request
}

func New() *Server {
	s := &Server{}
	s.api = httptest.NewServer(s.record("api", s.apiRoutes()))
	s.blob = httptest.NewServer(s.record("blob", http.HandlerFunc(s.serveBlob)))
	return s
}

func (s *Server) Load(runID int64, stage string) {
	s.runDir = Recording(runID, stage)
}

// Recording is the dir under testdata/recordings holding a run at a stage.
func Recording(runID int64, stage string) string {
	return filepath.Join(recordingsDir(), fmt.Sprintf("run-%d", runID), stage)
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
	mux.HandleFunc(repo+"/runs", func(w http.ResponseWriter, r *http.Request) {
		s.serveJSON(w, r, `{"total_count":1,"workflow_runs":[%s]}`, "run.json")
	})
	mux.HandleFunc(repo+"/runs/{id}/attempts/{n}", func(w http.ResponseWriter, r *http.Request) {
		s.serveJSON(w, r, "%s", "attempt-"+r.PathValue("n"), "attempt.json")
	})
	mux.HandleFunc(repo+"/runs/{id}/attempts/{n}/jobs", func(w http.ResponseWriter, r *http.Request) {
		s.serveJSON(w, r, "%s", "attempt-"+r.PathValue("n"), "jobs.json")
	})
	mux.HandleFunc(repo+"/jobs/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		blobURL := strings.Replace(s.blob.URL, "127.0.0.1", "localhost", 1) + "/logs/" + r.PathValue("id")
		http.Redirect(w, r, blobURL, http.StatusFound)
	})
	return mux
}

func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/logs/")
	matches, _ := filepath.Glob(filepath.Join(s.runDir, "attempt-*", "logs", id+".txt"))
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

// serveJSON serves a recorded file compacted, as GitHub serves it, inside wrapper.
func (s *Server) serveJSON(w http.ResponseWriter, r *http.Request, wrapper string, elem ...string) {
	recorded, err := os.ReadFile(filepath.Join(append([]string{s.runDir}, elem...)...))
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	var compact bytes.Buffer
	if err == nil {
		err = json.Compact(&compact, recorded)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = fmt.Fprintf(w, wrapper, compact.Bytes())
}

// recordingsDir finds testdata/recordings from this source file, so it works
// from any package's test binary.
func recordingsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "recordings")
}
