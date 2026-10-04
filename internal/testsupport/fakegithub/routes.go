package fakegithub

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	recordedRepo = "rosenhouse/lg"
	repoID       = "1402714635"
	repoBody     = `{"id":1402714635,"name":"Lg","full_name":"rosenhouse/Lg","default_branch":"main"}`
)

// routes mounts every route at the root and under /api/v3, and addresses
// the repo both as /repos/{owner}/{repo} and as /repositories/{repoid}.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	handle := func(route string, h http.HandlerFunc) {
		for _, mount := range []string{"", "/api/v3"} {
			for _, repo := range []string{"/repos/{owner}/{repo}", "/repositories/{repoid}"} {
				mux.HandleFunc("GET "+mount+repo+route, ofRepo(h))
			}
		}
	}
	handle("", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, []byte(repoBody)) })
	handle("/actions/runs", s.serveRuns)
	handle("/actions/runs/{id}", s.ofRun(func(w http.ResponseWriter, r *http.Request, run *run) {
		s.serveFile(w, r, run, "run.json")
	}))
	handle("/actions/runs/{id}/jobs", s.ofRun(func(w http.ResponseWriter, r *http.Request, run *run) {
		file := "jobs-latest.json"
		if r.URL.Query().Get("filter") == "all" {
			file = "jobs-all.json"
		}
		s.serveListing(w, r, run, file, "jobs")
	}))
	handle("/actions/runs/{id}/artifacts", s.ofRun(func(w http.ResponseWriter, r *http.Request, run *run) {
		s.serveListing(w, r, run, "artifacts.json", "artifacts")
	}))
	handle("/actions/runs/{id}/attempts/{n}", s.ofRun(func(w http.ResponseWriter, r *http.Request, run *run) {
		s.serveFile(w, r, run, filepath.Join("attempt-"+r.PathValue("n"), "attempt.json"))
	}))
	handle("/actions/runs/{id}/attempts/{n}/jobs", s.ofRun(func(w http.ResponseWriter, r *http.Request, run *run) {
		s.serveListing(w, r, run, filepath.Join("attempt-"+r.PathValue("n"), "jobs.json"), "jobs")
	}))
	handle("/actions/artifacts/{id}", s.serveArtifact)
	handle("/actions/runs/{id}/attempts/{n}/logs", s.serveDownload)
	handle("/actions/jobs/{id}/logs", s.serveDownload)
	handle("/actions/artifacts/{id}/zip", s.serveDownload)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { writeError(w, http.StatusNotFound) })
	return mux
}

// ofRepo answers 404 for any repo but the recorded one, which GitHub matches case-insensitively.
func ofRepo(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		named := strings.EqualFold(r.PathValue("owner")+"/"+r.PathValue("repo"), recordedRepo)
		if !named && r.PathValue("repoid") != repoID {
			writeError(w, http.StatusNotFound)
			return
		}
		h(w, r)
	}
}

// ofRun answers 404 for any run not loaded.
func (s *Server) ofRun(h func(http.ResponseWriter, *http.Request, *run)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		run := s.runs[r.PathValue("id")]
		s.mu.Unlock()
		if run == nil {
			writeError(w, http.StatusNotFound)
			return
		}
		h(w, r, run)
	}
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, run *run, file string) {
	body, err := served(filepath.Join(run.dir, file))
	if writeReadError(w, err) {
		return
	}
	writeJSON(w, body)
}

// serveListing serves a page of the array field of a recorded listing.
func (s *Server) serveListing(w http.ResponseWriter, r *http.Request, run *run, file, field string) {
	body, err := served(filepath.Join(run.dir, file))
	if writeReadError(w, err) {
		return
	}
	var recorded map[string]json.RawMessage
	var total int
	var elements []json.RawMessage
	err = json.Unmarshal(body, &recorded)
	if err == nil {
		err = errors.Join(json.Unmarshal(recorded["total_count"], &total), json.Unmarshal(recorded[field], &elements))
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.servePage(w, r, field, total, elements)
}

// serveArtifact serves an artifact's element of its run's current listing.
func (s *Server) serveArtifact(w http.ResponseWriter, r *http.Request) {
	for _, run := range s.loaded() {
		body, err := served(filepath.Join(run.dir, "artifacts.json"))
		if err != nil {
			continue
		}
		var listing struct{ Artifacts []json.RawMessage }
		if err := json.Unmarshal(body, &listing); err != nil {
			continue
		}
		for _, a := range listing.Artifacts {
			var meta struct{ ID json.Number }
			if json.Unmarshal(a, &meta) == nil && meta.ID.String() == r.PathValue("id") {
				writeJSON(w, a)
				return
			}
		}
	}
	writeError(w, http.StatusNotFound)
}

// serveDownload answers as status.txt recorded: a redirect to the blob
// host, or the recorded body with the recorded status.
func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	_, path, _ := strings.Cut(r.URL.Path, "/actions/")
	for id, run := range s.loaded() {
		d, ok := run.downloads[path]
		if !ok {
			continue
		}
		if d.First == http.StatusFound {
			blob := strings.Replace(s.blob.URL, "127.0.0.1", "localhost", 1)
			http.Redirect(w, r, blob+"/"+id+"/"+filepath.ToSlash(d.file), http.StatusFound)
			return
		}
		body, err := served(filepath.Join(run.dir, d.file))
		if writeReadError(w, err) {
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(d.First)
		_, _ = w.Write(body)
		return
	}
	writeError(w, http.StatusNotFound)
}

// serveBlob serves a recorded body with its final status. Like blob
// storage, it refuses a request carrying GitHub's Authorization.
func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		refuseAuthorization(w, auth)
		return
	}
	id, file, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	s.mu.Lock()
	run := s.runs[id]
	s.mu.Unlock()
	if run == nil {
		http.NotFound(w, r)
		return
	}
	d, ok := run.blobs[filepath.FromSlash(file)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := os.ReadFile(filepath.Join(run.dir, d.file))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(d.Final)
	_, _ = w.Write(body)
}

// refuseAuthorization answers as blob storage does: 401 to a Bearer token
// it cannot parse, 403 to any other scheme.
func refuseAuthorization(w http.ResponseWriter, auth string) {
	w.Header().Set("Content-Type", "application/xml")
	if strings.HasPrefix(auth, "Bearer ") {
		w.Header().Set("WWW-Authenticate", "Bearer authorization_uri=https://login.microsoftonline.com/common/oauth2/authorize resource_id=https://storage.azure.com")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("\uFEFF" + `<?xml version="1.0" encoding="utf-8"?><Error><Code>InvalidAuthenticationInfo</Code><Message>Server failed to authenticate the request. Please refer to the information in the www-authenticate header.</Message><AuthenticationErrorDetail>The access token was missing or malformed.</AuthenticationErrorDetail></Error>`))
		return
	}
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte("\uFEFF" + `<?xml version="1.0" encoding="utf-8"?>` + "\n" + `<Error><Code>AuthenticationFailed</Code><Message>Server failed to authenticate the request. Make sure the value of Authorization header is formed correctly including the signature.</Message></Error>`))
}

func (s *Server) loaded() map[string]*run {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := make(map[string]*run, len(s.runs))
	for id, run := range s.runs {
		runs[id] = run
	}
	return runs
}

// writeReadError answers 404 for a file not recorded, or 500, and reports
// whether it answered.
func writeReadError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	return err != nil
}

func writeJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}
