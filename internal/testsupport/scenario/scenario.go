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
	return RerunAt(r, attempt, r.runStartedAt(attempt).Add(Day))
}

// RerunAt moves every time at or after attempt's run_started_at by the same
// amount, so that the attempt started at the given time. It panics when at is
// earlier, since the jobs the attempt carried forward must start before it.
func RerunAt(r Run, attempt int, at time.Time) Run {
	started := r.runStartedAt(attempt)
	if at.Before(started) {
		panic(fmt.Sprintf("attempt %d started at %s, after %s", attempt, started, at))
	}
	return r.shiftTimes(func(t time.Time) bool { return !t.Before(started) }, at.Sub(started))
}

func (r Run) runStartedAt(attempt int) time.Time {
	var started struct {
		RunStartedAt time.Time `json:"run_started_at"`
	}
	mustUnmarshal(r.Files[attemptFile(attempt, "attempt.json")].Data, &started)
	return started.RunStartedAt
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

// Cancel concludes attempt, and the run when attempt is its latest, cancelled.
func Cancel(r Run, attempt int) Run {
	return r.conclude(attempt, "completed", "cancelled")
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

// RenameJob renames attempt's jobs named name to newName.
func RenameJob(r Run, attempt int, name, newName string) Run {
	out := r.copy()
	out.editJobs(attempt, func(jobs []any) []any {
		for _, job := range jobs {
			if job := job.(map[string]any); job["name"] == name {
				job["name"] = newName
			}
		}
		return jobs
	})
	return out
}

// RenumberJob gives attempt's job named name, and its log, the id.
func RenumberJob(r Run, attempt int, name string, id int64) Run {
	out := r.copy()
	out.editJobs(attempt, func(jobs []any) []any {
		for _, job := range jobs {
			job := job.(map[string]any)
			if job["name"] != name {
				continue
			}
			log := attemptFile(attempt, fmt.Sprintf("logs/%s.txt", job["id"]))
			out.Files[attemptFile(attempt, fmt.Sprintf("logs/%d.txt", id))] = out.Files[log]
			delete(out.Files, log)
			status := out.Files["status.txt"]
			status.Data = bytes.ReplaceAll(status.Data, fmt.Appendf(nil, " jobs/%s/logs\n", job["id"]), fmt.Appendf(nil, " jobs/%d/logs\n", id))
			job["id"] = id
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
	out.editArtifacts(func(artifact map[string]any) {
		if artifact["id"].(json.Number).String() == strconv.FormatInt(artifactID, 10) {
			edit(artifact)
		}
	})
	return out
}

// WithPullRequests lists the run, and every attempt, with pull requests of the given numbers.
func WithPullRequests(r Run, numbers ...int) Run {
	prs := make([]any, len(numbers))
	for i, n := range numbers {
		prs[i] = map[string]any{"number": n}
	}
	return r.editRuns(func(run map[string]any) { run["pull_requests"] = prs })
}

// OnBranch sets head_branch in the run, every attempt, their jobs and the artifacts' workflow_run.
func OnBranch(r Run, branch string) Run { return r.setHead("head_branch", branch) }

// WithSHA sets head_sha in the run, every attempt, their jobs and the artifacts' workflow_run.
func WithSHA(r Run, sha string) Run { return r.setHead("head_sha", sha) }

func (r Run) setHead(key, value string) Run {
	out := r.editRuns(func(run map[string]any) { run[key] = value })
	for n := 1; out.Files[attemptFile(n, "jobs.json")] != nil; n++ {
		out.editJobs(n, func(jobs []any) []any {
			for _, job := range jobs {
				job.(map[string]any)[key] = value
			}
			return jobs
		})
	}
	out.editArtifacts(func(artifact map[string]any) { artifact["workflow_run"].(map[string]any)[key] = value })
	return out
}

// WithEvent sets the event of the run and every attempt.
func WithEvent(r Run, event string) Run {
	return r.editRuns(func(run map[string]any) { run["event"] = event })
}

// WithDisplayTitle sets the display_title of the run and every attempt.
func WithDisplayTitle(r Run, title string) Run {
	return r.editRuns(func(run map[string]any) { run["display_title"] = title })
}

// forkID is the repository id of every fork.
const forkID = 1

// FromFork gives the run, every attempt and the artifacts' workflow_run the
// head repository fullName, which is not the repository.
func FromFork(r Run, fullName string) Run {
	out := r.editRuns(func(run map[string]any) {
		run["head_repository"] = map[string]any{"id": forkID, "full_name": fullName}
	})
	out.editArtifacts(func(artifact map[string]any) {
		artifact["workflow_run"].(map[string]any)["head_repository_id"] = forkID
	})
	return out
}

// InjectLogLine puts a line holding text first in the log of each job of
// that name in the attempt: after the BOM, with the timestamp of the log's
// first line.
func InjectLogLine(r Run, attempt int, job, text string) Run {
	var listing struct {
		Jobs []struct {
			ID   int64
			Name string
		}
	}
	mustUnmarshal(r.Files[attemptFile(attempt, "jobs.json")].Data, &listing)
	out := r.copy()
	injected := false
	for _, j := range listing.Jobs {
		log := out.Files[attemptFile(attempt, fmt.Sprintf("logs/%d.txt", j.ID))]
		if j.Name != job || log == nil {
			continue
		}
		rest, ok := bytes.CutPrefix(log.Data, []byte(bom))
		stamp, _, found := bytes.Cut(rest, []byte(" "))
		if !ok || !found {
			panic(fmt.Sprintf("log of job %d starts with no BOM and timestamp", j.ID))
		}
		log.Data = slices.Concat([]byte(bom), stamp, []byte(" "+text+"\n"), rest)
		injected = true
	}
	if !injected {
		panic(fmt.Sprintf("attempt %d has no log of a job named %q", attempt, job))
	}
	return out
}

const bom = "\uFEFF"

// editRuns edits run.json and every attempt's attempt.json.
func (r Run) editRuns(edit func(map[string]any)) Run {
	out := r.copy()
	out.edit("run.json", edit)
	for n := 1; out.Files[attemptFile(n, "attempt.json")] != nil; n++ {
		out.edit(attemptFile(n, "attempt.json"), edit)
	}
	return out
}

func (r Run) editArtifacts(edit func(map[string]any)) {
	r.edit("artifacts.json", func(listing map[string]any) {
		for _, artifact := range listing["artifacts"].([]any) {
			edit(artifact.(map[string]any))
		}
	})
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

// Day is 24 hours, for times relative to recordings.DefaultNow.
const Day = 24 * time.Hour

// fixtureRun is the recorded run that specs clone.
const fixtureRun = 37129390741

// CloneAt is fixtureRun at stage, as run id created at the given time.
func CloneAt(id int64, stage string, at time.Time) Run {
	return CreatedAt(Clone(Recorded(fixtureRun, stage), id), at)
}

// CreatedAt moves every time in r's JSON files by the same amount, so that
// the run was created at the given time.
func CreatedAt(r Run, at time.Time) Run {
	return r.shiftTimes(func(time.Time) bool { return true }, at.Sub(r.createdAt()))
}

func (r Run) createdAt() time.Time {
	var run struct {
		CreatedAt time.Time `json:"created_at"`
	}
	mustUnmarshal(r.Files["run.json"].Data, &run)
	return run.CreatedAt
}

// shiftTimes moves every time in r's JSON files that moved accepts by delta.
func (r Run) shiftTimes(moved func(time.Time) bool, delta time.Duration) Run {
	out := r.copy()
	for name, file := range out.Files {
		if path.Ext(name) != ".json" {
			continue
		}
		file.Data = timestamp.ReplaceAllFunc(file.Data, func(quoted []byte) []byte {
			var t time.Time
			mustUnmarshal(quoted, &t)
			if !moved(t) {
				return quoted
			}
			return []byte(t.Add(delta).Format(`"2006-01-02T15:04:05Z"`))
		})
	}
	return out
}

// ListedRun is the body of a completed run with only the fields a listing needs.
func ListedRun(id int64, createdAt time.Time) json.RawMessage {
	return listedRun(id, createdAt, "completed", `"success"`)
}

// QueuedRun is the body of a queued run with only the fields a listing needs.
func QueuedRun(id int64, createdAt time.Time) json.RawMessage {
	return listedRun(id, createdAt, "queued", "null")
}

func listedRun(id int64, createdAt time.Time, status, conclusion string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":%d,"created_at":%q,"status":%q,"conclusion":%s,"run_attempt":1,"repository":{"full_name":"rosenhouse/Lg"}}`,
		id, createdAt.UTC().Format(time.RFC3339), status, conclusion))
}

// JobIDs lists the ids of attempt's jobs named name, in listing order.
func (r Run) JobIDs(attempt int, name string) []int64 {
	var listing struct {
		Jobs []struct {
			ID   int64
			Name string
		}
	}
	mustUnmarshal(r.Files[attemptFile(attempt, "jobs.json")].Data, &listing)
	var ids []int64
	for _, job := range listing.Jobs {
		if job.Name == name {
			ids = append(ids, job.ID)
		}
	}
	return ids
}

// SetJobConclusion concludes attempt's job of that id so.
func SetJobConclusion(r Run, attempt int, jobID int64, conclusion string) Run {
	return r.editJob(attempt, jobID, func(job map[string]any) { job["conclusion"] = conclusion })
}

// SetStepConclusion concludes the step of that name in attempt's job of that id so.
func SetStepConclusion(r Run, attempt int, jobID int64, step, conclusion string) Run {
	return r.editStep(attempt, jobID, step, func(s map[string]any) { s["conclusion"] = conclusion })
}

// RenameStep renames the step of that name in attempt's job of that id.
func RenameStep(r Run, attempt int, jobID int64, step, newName string) Run {
	return r.editStep(attempt, jobID, step, func(s map[string]any) { s["name"] = newName })
}

func (r Run) editStep(attempt int, jobID int64, step string, edit func(map[string]any)) Run {
	return r.editJob(attempt, jobID, func(job map[string]any) {
		found := false
		for _, s := range job["steps"].([]any) {
			if s := s.(map[string]any); s["name"] == step {
				edit(s)
				found = true
			}
		}
		if !found {
			panic(fmt.Sprintf("attempt %d's job %d has no step named %q", attempt, jobID, step))
		}
	})
}

// ClearSteps leaves attempt's job of that id no steps.
func ClearSteps(r Run, attempt int, jobID int64) Run {
	return r.editJob(attempt, jobID, func(job map[string]any) { job["steps"] = []any{} })
}

// ReverseSteps lists the steps of attempt's job of that id in reverse.
func ReverseSteps(r Run, attempt int, jobID int64) Run {
	return r.editJob(attempt, jobID, func(job map[string]any) { slices.Reverse(job["steps"].([]any)) })
}

func (r Run) editJob(attempt int, jobID int64, edit func(map[string]any)) Run {
	out := r.copy()
	found := false
	out.editJobs(attempt, func(jobs []any) []any {
		for _, job := range jobs {
			if job := job.(map[string]any); job["id"].(json.Number).String() == strconv.FormatInt(jobID, 10) {
				edit(job)
				found = true
			}
		}
		return jobs
	})
	if !found {
		panic(fmt.Sprintf("attempt %d has no job %d", attempt, jobID))
	}
	return out
}

// AddRerunAttempt adds an attempt, a minute after the latest ends, that
// re-runs the jobs named and carries every other job forward. Each job gets
// a new id, its URLs and its log; the re-run jobs and all their steps succeed.
func AddRerunAttempt(r Run, jobs ...string) Run {
	out := r.copy()
	var run struct {
		RunAttempt int `json:"run_attempt"`
	}
	mustUnmarshal(out.Files["run.json"].Data, &run)
	latest, next := run.RunAttempt, run.RunAttempt+1
	for _, name := range jobs {
		if len(out.JobIDs(latest, name)) == 0 {
			panic(fmt.Sprintf("attempt %d has no job named %q", latest, name))
		}
	}
	var ended struct {
		UpdatedAt time.Time `json:"updated_at"`
	}
	mustUnmarshal(out.Files[attemptFile(latest, "attempt.json")].Data, &ended)
	started := ended.UpdatedAt.Add(time.Minute)

	var listing map[string]any
	mustUnmarshal(out.Files[attemptFile(latest, "jobs.json")].Data, &listing)
	id := out.maxJobID()
	conclusion := "success"
	for _, j := range listing["jobs"].([]any) {
		job := j.(map[string]any)
		id++
		old, renumbered := job["id"].(json.Number).String(), strconv.FormatInt(id, 10)
		job["id"], job["run_attempt"] = json.Number(renumbered), next
		for _, key := range []string{"url", "html_url", "check_run_url"} {
			job[key] = strings.Replace(job[key].(string), old, renumbered, 1)
		}
		if slices.Contains(jobs, job["name"].(string)) {
			rerunJob(job, started.Add(2*time.Second))
		}
		if failing(job["conclusion"]) {
			conclusion = "failure"
		}
		out.copyLog(latest, old, next, renumbered)
	}
	out.Files[attemptFile(next, "jobs.json")] = &fstest.MapFile{Data: mustMarshal(listing)}
	out.Files[attemptFile(next, "attempt.json")] = &fstest.MapFile{Data: bytes.Clone(out.Files[attemptFile(latest, "attempt.json")].Data)}
	conclude := func(run map[string]any) {
		run["run_attempt"], run["status"], run["conclusion"] = next, "completed", conclusion
		run["run_started_at"], run["updated_at"] = started.Format(time.RFC3339), started.Add(2*time.Minute).Format(time.RFC3339)
		run["previous_attempt_url"] = fmt.Sprintf("%s/attempts/%d", run["url"], latest)
	}
	out.edit(attemptFile(next, "attempt.json"), func(attempt map[string]any) {
		conclude(attempt)
		for _, key := range []string{"jobs_url", "logs_url"} {
			attempt[key] = strings.Replace(attempt[key].(string), fmt.Sprintf("/attempts/%d/", latest), fmt.Sprintf("/attempts/%d/", next), 1)
		}
	})
	out.edit("run.json", conclude)
	return out
}

// rerunJob moves the job's times so that it starts at start, and concludes
// it and its steps success.
func rerunJob(job map[string]any, start time.Time) {
	delta := start.Sub(parseTime(job["started_at"]))
	rerun := func(m map[string]any) {
		for _, key := range []string{"created_at", "started_at", "completed_at"} {
			if _, ok := m[key].(string); ok {
				m[key] = parseTime(m[key]).Add(delta).Format(time.RFC3339)
			}
		}
		m["conclusion"] = "success"
	}
	rerun(job)
	for _, step := range job["steps"].([]any) {
		rerun(step.(map[string]any))
	}
}

func parseTime(v any) time.Time {
	t, err := time.Parse(time.RFC3339, v.(string))
	if err != nil {
		panic(err)
	}
	return t
}

func failing(conclusion any) bool {
	return slices.Contains([]any{"failure", "cancelled", "timed_out"}, conclusion)
}

// copyLog serves job old's log of attempt from as job id's of attempt to, with the same statuses.
func (r Run) copyLog(from int, old string, to int, id string) {
	log := r.Files[attemptFile(from, "logs/"+old+".txt")]
	line := regexp.MustCompile(`(?m)^(\S+) jobs/` + old + `/logs$`).FindSubmatch(r.Files["status.txt"].Data)
	if log == nil || line == nil {
		return
	}
	r.Files[attemptFile(to, "logs/"+id+".txt")] = &fstest.MapFile{Data: bytes.Clone(log.Data)}
	status := r.Files["status.txt"]
	status.Data = fmt.Appendf(status.Data, "%s jobs/%s/logs\n", line[1], id)
}

// maxJobID is the highest id of any job of any attempt of r.
func (r Run) maxJobID() int64 {
	var highest int64
	for n := 1; r.Files[attemptFile(n, "jobs.json")] != nil; n++ {
		var listing struct{ Jobs []struct{ ID int64 } }
		mustUnmarshal(r.Files[attemptFile(n, "jobs.json")].Data, &listing)
		for _, job := range listing.Jobs {
			highest = max(highest, job.ID)
		}
	}
	return highest
}

// EndingAt moves every time of the runs by the same amount, so that the
// newest was created at the given time.
func EndingAt(runs []Run, at time.Time) []Run {
	var newest time.Time
	for _, r := range runs {
		if created := r.createdAt(); created.After(newest) {
			newest = created
		}
	}
	moved := make([]Run, len(runs))
	for i, r := range runs {
		moved[i] = r.shiftTimes(func(time.Time) bool { return true }, at.Sub(newest))
	}
	return moved
}

// WithArtifactZip serves zip as the artifact's zip, listing its size and digest.
func WithArtifactZip(r Run, artifactID int64, zip []byte) Run {
	return r
}
