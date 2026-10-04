// Package scenario derives runs from the recordings, as files that
// fakegithub serves. Every function returns a new Run and changes none it is given.
package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"
	"time"

	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

// Run is a run at one stage: the files record.sh writes, by slash path.
type Run struct {
	ID    int64
	Files fstest.MapFS
}

// Recorded reads the recording of a run at a stage.
func Recorded(runID int64, stage string) Run {
	recording := os.DirFS(recordings.Dir(runID, stage))
	files := fstest.MapFS{}
	err := fs.WalkDir(recording, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(recording, name)
		files[name] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		panic(err)
	}
	return Run{ID: runID, Files: files}
}

// maxCloneID keeps every cloned id below 2^63.
const maxCloneID = 1_000_000

// idSpace exceeds every recorded job and artifact id.
const idSpace = 1_000_000_000_000

// Clone gives the run id, and each job and artifact x the id id*10^12+x,
// so clones of one run at different stages agree. Logs and zips keep their bytes.
func Clone(src Run, id int64) Run {
	if id <= 0 || id >= maxCloneID {
		panic(fmt.Sprintf("clone id %d is not in (0, %d)", id, maxCloneID))
	}
	newIDs := map[int64]int64{src.ID: id}
	for _, old := range src.ids() {
		newIDs[old] = id*idSpace + old
	}
	// Longest first, so that no id is replaced by a prefix of it.
	var pairs []string
	for _, old := range slices.Backward(slices.Sorted(maps.Keys(newIDs))) {
		pairs = append(pairs, strconv.FormatInt(old, 10), strconv.FormatInt(newIDs[old], 10))
	}
	ids := strings.NewReplacer(pairs...)
	files := fstest.MapFS{}
	for name, file := range src.Files {
		data := file.Data
		if path.Ext(name) == ".json" || name == "status.txt" {
			data = []byte(ids.Replace(string(data)))
		}
		files[path.Join(path.Dir(name), ids.Replace(path.Base(name)))] = &fstest.MapFile{Data: data}
	}
	return Run{ID: id, Files: files}
}

// ids lists the ids of every job and artifact of r.
func (r Run) ids() []int64 {
	var ids []int64
	for name, file := range r.Files {
		var listing struct{ Jobs, Artifacts []struct{ ID int64 } }
		if path.Ext(name) != ".json" || json.Unmarshal(file.Data, &listing) != nil {
			continue
		}
		for _, element := range append(listing.Jobs, listing.Artifacts...) {
			ids = append(ids, element.ID)
		}
	}
	return ids
}

var timestamp = regexp.MustCompile(`"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ"`)

// NextDayRerun moves every time at or after attempt's run_started_at 24
// hours later, so the attempt and the jobs it ran start the next UTC day.
func NextDayRerun(r Run, attempt int) Run {
	var started struct {
		RunStartedAt time.Time `json:"run_started_at"`
	}
	mustUnmarshal(r.Files[attemptFile(attempt, "attempt.json")].Data, &started)
	out := r.copy()
	for name, file := range out.Files {
		if path.Ext(name) != ".json" {
			continue
		}
		file.Data = timestamp.ReplaceAllFunc(file.Data, func(quoted []byte) []byte {
			var t time.Time
			mustUnmarshal(quoted, &t)
			if t.Before(started.RunStartedAt) {
				return quoted
			}
			return []byte(t.Add(24 * time.Hour).Format(`"2006-01-02T15:04:05Z"`))
		})
	}
	return out
}

// RenameWorkflow renames the workflow to name in run.json, in attempt and
// every later attempt, and in their jobs.
func RenameWorkflow(r Run, attempt int, name string) Run {
	out := r.copy()
	out.edit("run.json", func(run map[string]any) { run["name"] = name })
	for n := attempt; out.Files[attemptFile(n, "attempt.json")] != nil; n++ {
		out.edit(attemptFile(n, "attempt.json"), func(run map[string]any) { run["name"] = name })
		out.editJobs(n, func(jobs []any) []any {
			for _, job := range jobs {
				job.(map[string]any)["workflow_name"] = name
			}
			return jobs
		})
	}
	return out
}

// InProgress leaves attempt, and the run when attempt is its latest, in progress.
func InProgress(r Run, attempt int) Run {
	return r.conclude(attempt, "in_progress", nil)
}

// StartupFailure concludes attempt startup_failure, with no jobs and no logs.
func StartupFailure(r Run, attempt int) Run {
	out := r.conclude(attempt, "completed", "startup_failure")
	out.editJobs(attempt, func([]any) []any { return []any{} })
	for name := range out.Files {
		if strings.HasPrefix(name, attemptFile(attempt, "logs/")) {
			delete(out.Files, name)
		}
	}
	return out
}

// QueueJob leaves attempt's jobs named name queued, with no steps, runner,
// start, end or conclusion.
func QueueJob(r Run, attempt int, name string) Run {
	out := r.copy()
	out.editJobs(attempt, func(jobs []any) []any {
		for _, job := range jobs {
			job := job.(map[string]any)
			if job["name"] == name {
				job["status"], job["steps"] = "queued", []any{}
				job["conclusion"], job["runner_name"], job["started_at"], job["completed_at"] = nil, nil, nil, nil
			}
		}
		return jobs
	})
	return out
}

// Expire lists the artifact as expired: true, as the docs describe. No recording shows it.
func Expire(r Run, artifactID int64) Run {
	return r.editArtifact(artifactID, func(artifact map[string]any) { artifact["expired"] = true })
}

// ListsArtifact reports whether r's artifacts.json lists the artifact.
func (r Run) ListsArtifact(artifactID int64) bool {
	var listing struct{ Artifacts []struct{ ID int64 } }
	mustUnmarshal(r.Files["artifacts.json"].Data, &listing)
	return slices.ContainsFunc(listing.Artifacts, func(a struct{ ID int64 }) bool { return a.ID == artifactID })
}

// WithoutDigest lists the artifact with no digest.
func WithoutDigest(r Run, artifactID int64) Run {
	return r.editArtifact(artifactID, func(artifact map[string]any) { delete(artifact, "digest") })
}

// WithDigest lists the artifact with the digest.
func WithDigest(r Run, artifactID int64, digest string) Run {
	return r.editArtifact(artifactID, func(artifact map[string]any) { artifact["digest"] = digest })
}

func (r Run) editArtifact(artifactID int64, edit func(map[string]any)) Run {
	out := r.copy()
	out.edit("artifacts.json", func(listing map[string]any) {
		for _, artifact := range listing["artifacts"].([]any) {
			artifact := artifact.(map[string]any)
			if artifact["id"].(json.Number).String() == strconv.FormatInt(artifactID, 10) {
				edit(artifact)
			}
		}
	})
	return out
}

// WithPullRequests lists the run with pull requests of the given numbers.
func WithPullRequests(r Run, numbers ...int) Run {
	prs := make([]any, len(numbers))
	for i, n := range numbers {
		prs[i] = map[string]any{"number": n}
	}
	out := r.copy()
	out.edit("run.json", func(run map[string]any) { run["pull_requests"] = prs })
	return out
}

// WithoutRunAttempt drops run_attempt from the listed run.
func WithoutRunAttempt(r Run) Run {
	out := r.copy()
	out.edit("run.json", func(run map[string]any) { delete(run, "run_attempt") })
	return out
}

func (r Run) conclude(attempt int, status string, conclusion any) Run {
	out := r.copy()
	set := func(run map[string]any) { run["status"], run["conclusion"] = status, conclusion }
	out.edit(attemptFile(attempt, "attempt.json"), set)
	var latest struct {
		RunAttempt int `json:"run_attempt"`
	}
	mustUnmarshal(out.Files["run.json"].Data, &latest)
	if latest.RunAttempt == attempt {
		out.edit("run.json", set)
	}
	return out
}

func attemptFile(attempt int, name string) string {
	return fmt.Sprintf("attempt-%d/%s", attempt, name)
}

// copy copies r's files, so that edits leave r unchanged.
func (r Run) copy() Run {
	files := maps.Clone(r.Files)
	for name, file := range files {
		files[name] = &fstest.MapFile{Data: bytes.Clone(file.Data)}
	}
	return Run{ID: r.ID, Files: files}
}

func (r Run) edit(name string, edit func(map[string]any)) {
	var object map[string]any
	mustUnmarshal(r.Files[name].Data, &object)
	edit(object)
	r.Files[name].Data = mustMarshal(object)
}

func (r Run) editJobs(attempt int, edit func([]any) []any) {
	r.edit(attemptFile(attempt, "jobs.json"), func(listing map[string]any) {
		jobs := edit(listing["jobs"].([]any))
		listing["jobs"], listing["total_count"] = jobs, len(jobs)
	})
}

func mustUnmarshal(data []byte, v any) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(v); err != nil {
		panic(err)
	}
}

// mustMarshal leaves <, > and & as GitHub sends them.
func mustMarshal(v any) []byte {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}
