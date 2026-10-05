package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

// liveSyncTimeout bounds one sync of every run github.com lists for rosenhouse/Lg.
const liveSyncTimeout = 15 * time.Minute

const (
	fixtureStage       = "after-attempt-3"
	fixtureLastAttempt = 3
	logsDeletedStage   = "after-expiry"
	// GitHub delists an artifact once it expires, so the fixture run's live listing is the after-expiry one.
	artifactsStage  = "after-expiry"
	expiredArtifact = int64(11276327411)
)

// recordedKinds counts the job kinds of each recorded attempt, by run and attempt.
var recordedKinds = map[int64]map[int]map[model.JobKind]int{
	fixtureRun: {
		1: {model.Ran: 10, model.NotApplicable: 2},
		2: {model.CarriedForward: 8, model.NotApplicable: 1, model.Ran: 3},
		3: {model.Ran: 11, model.NotApplicable: 1},
	},
	logsDeletedRun: {
		1: {model.Ran: 10, model.NotApplicable: 2},
	},
}

var _ = Describe("lg sync against github.com/rosenhouse/Lg", Label("live"), Ordered, func() {
	var (
		env            *harness.Env
		fixtureDir     string
		logsDeletedDir string
	)

	BeforeAll(func() {
		env = harness.NewLive(lgPath)
		window := liveWindow()
		env.WriteLiveConfig("backfill: "+window, "retention: "+window)

		syncLive(env)
		fixtureDir = filepath.Join(env.Data(), fixtureRunDir)
		logsDeletedDir = runDir(env, logsDeletedRun)
	})

	It("mirrors run 37129390741 with attempts 1–3, the recorded job ids and the recorded job kinds", func() {
		for attempt := 1; attempt <= fixtureLastAttempt; attempt++ {
			dir := layout.AttemptDir(fixtureDir, attempt)
			Expect(dir).To(BeADirectory())
			kinds := storedKinds(dir)
			Expect(slices.Collect(maps.Keys(kinds))).To(ConsistOf(recordedJobIDs(fixtureRun, fixtureStage, attempt)), "attempt %d", attempt)
			Expect(countKinds(kinds)).To(Equal(recordedKinds[fixtureRun][attempt]), "attempt %d", attempt)
		}
	})

	It("stores each log byte-identical to the recording, or a tombstone with reason expired or deleted", func() {
		for attempt := 1; attempt <= fixtureLastAttempt; attempt++ {
			dir := layout.AttemptDir(fixtureDir, attempt)
			logs := recordedLogs(fixtureRun, fixtureStage, attempt)
			ran := 0
			for id, kind := range storedKinds(dir) {
				if kind != model.Ran {
					continue
				}
				ran++
				Expect(logs).To(HaveKey(id), "attempt %d job %d", attempt, id)
				Expect(recordings.CompareLog(logs[id], jobDir(dir, strconv.FormatInt(id, 10)))).To(Succeed(), "attempt %d job %d", attempt, id)
			}
			Expect(ran).To(Equal(recordedKinds[fixtureRun][attempt][model.Ran]), "attempt %d", attempt)
		}
	})

	It("writes deleted tombstones for the 10 ran jobs of run 37129738159", func() {
		dir := layout.AttemptDir(logsDeletedDir, 1)
		kinds := storedKinds(dir)
		Expect(countKinds(kinds)).To(Equal(recordedKinds[logsDeletedRun][1]))
		for id, kind := range kinds {
			if kind != model.Ran {
				continue
			}
			job := jobDir(dir, strconv.FormatInt(id, 10))
			Expect(filepath.Join(job, "log.txt")).NotTo(BeAnExistingFile())
			Expect(readJSON(filepath.Join(job, "log.txt.tombstone"))).To(HaveKeyWithValue("reason", "deleted"), "job %d", id)
		}
	})

	It("stores attempt.json matching the recording on id, run_attempt, head_sha, status, conclusion and run_started_at", func() {
		compare := func(runDir string, runID int64, stage string, attempt int) {
			GinkgoHelper()
			want := readJSON(filepath.Join(recordings.Dir(runID, stage), fmt.Sprintf("attempt-%d", attempt), "attempt.json"))
			got := readJSON(filepath.Join(layout.AttemptDir(runDir, attempt), "attempt.json"))
			for _, key := range []string{"id", "run_attempt", "head_sha", "status", "conclusion", "run_started_at"} {
				Expect(got).To(HaveKeyWithValue(key, want[key]), "run %d attempt %d", runID, attempt)
			}
		}
		for attempt := 1; attempt <= fixtureLastAttempt; attempt++ {
			compare(fixtureDir, fixtureRun, fixtureStage, attempt)
		}
		compare(logsDeletedDir, logsDeletedRun, logsDeletedStage, 1)
	})

	It("lists the artifacts of run 37129390741 as the after-expiry recording does, without the expired artifact 11276327411", func() {
		recorded := unexpiredRecordedArtifacts()
		listed := artifactIDs(storedArtifacts(fixtureDir, fixtureLastAttempt))
		Expect(listed).To(ConsistOf(artifactIDs(recorded)))
		Expect(listed).NotTo(ContainElement(expiredArtifact))
	})

	It("writes a zip, or an expired or deleted tombstone, for every artifact listed for run 37129390741", func() {
		unexpiredRecordedArtifacts()
		listed := storedArtifacts(fixtureDir, fixtureLastAttempt)
		Expect(listed).NotTo(BeEmpty())

		for _, artifact := range listed {
			dir := layout.ArtifactDir(fixtureDir, artifact.ID, artifact.Name)
			zip, err := os.ReadFile(filepath.Join(dir, "artifact.zip"))
			if err == nil {
				recorded := filepath.Join(recordings.Dir(fixtureRun, fixtureStage), "artifacts", fmt.Sprintf("%d.zip", artifact.ID))
				Expect(os.ReadFile(recorded)).To(Equal(zip), "artifact %d", artifact.ID)
				continue
			}
			Expect(readJSON(filepath.Join(dir, "artifact.zip.tombstone"))).To(HaveKeyWithValue("reason", BeElementOf("expired", "deleted")), "artifact %d", artifact.ID)
		}
	})
})

var _ = Describe("the GitHub API", Label("live"), func() {
	It("returns compact JSON to lg's User-Agent, as the fake does", func() {
		fake := fakegithub.Start(fixtureRun, fixtureStage)
		for _, base := range []string{"https://api.github.com", fake.URL()} {
			body := get(base + "/repos/rosenhouse/Lg/actions/runs/37129390741")
			var compact bytes.Buffer
			Expect(json.Compact(&compact, body)).To(Succeed())
			Expect(string(body)).To(Equal(compact.String()), base)
		}
	})
})

// liveWindow is 90d, or longer once a fixture run is older, so the fixture
// runs stay in backfill and retention as they age.
func liveWindow() string {
	GinkgoHelper()
	days := 90
	now := clock.Real{}.Now()
	for runID, stage := range map[int64]string{fixtureRun: fixtureStage, logsDeletedRun: logsDeletedStage} {
		run, err := recordings.Attempt(runID, stage, 1)
		Expect(err).NotTo(HaveOccurred())
		days = max(days, int(now.Sub(run.CreatedAt).Hours()/24)+2)
	}
	return fmt.Sprintf("%dd", days)
}

// unexpiredRecordedArtifacts fails once the recorded artifacts expire,
// since GitHub then lists none and the fixture runs need re-recording.
func unexpiredRecordedArtifacts() []model.Artifact {
	GinkgoHelper()
	recorded, err := recordings.Artifacts(fixtureRun, artifactsStage)
	Expect(err).NotTo(HaveOccurred())
	now := clock.Real{}.Now()
	for _, artifact := range recorded {
		Expect(now).To(BeTemporally("<", artifact.ExpiresAt), "artifact %d expired; re-record the fixture runs", artifact.ID)
	}
	return recorded
}

// syncLive retries, because one transient GitHub error on any run fails a
// sync, and the next sync resumes where it stopped.
func syncLive(env *harness.Env) {
	GinkgoHelper()
	var session *gexec.Session
	for range 3 {
		if session = env.Lg("sync").Wait(liveSyncTimeout); session.ExitCode() == 0 {
			break
		}
	}
	Expect(session).To(gexec.Exit(0))
}

func get(url string) []byte {
	GinkgoHelper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("User-Agent", github.UserAgent())
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	Expect(resp.StatusCode).To(Equal(http.StatusOK), url)
	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return body
}

// runDir finds runs/<date>/<id>_* whatever the date and slugs.
func runDir(env *harness.Env, runID int64) string {
	GinkgoHelper()
	matches, err := filepath.Glob(filepath.Join(env.Data(), "github.com/rosenhouse/Lg/runs/*", fmt.Sprintf("%d_*", runID)))
	Expect(err).NotTo(HaveOccurred())
	Expect(matches).To(HaveLen(1), "run dir for %d", runID)
	return matches[0]
}

func recordedJobIDs(runID int64, stage string, attempt int) []int64 {
	GinkgoHelper()
	jobs, err := recordings.Jobs(runID, stage, attempt)
	Expect(err).NotTo(HaveOccurred())
	var out []int64
	for _, job := range jobs {
		out = append(out, job.ID)
	}
	return out
}

// recordedLogs are the log bodies GitHub served for a recorded attempt, by
// job id. record.sh fetches every job's log, carried-forward ones included,
// and writes the body of a 404 too, so status.txt says which are logs.
func recordedLogs(runID int64, stage string, attempt int) map[int64][]byte {
	GinkgoHelper()
	status, err := os.Open(filepath.Join(recordings.Dir(runID, stage), "status.txt"))
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = status.Close() }()
	lines, err := recordings.ParseStatus(status)
	Expect(err).NotTo(HaveOccurred())
	logs := map[int64][]byte{}
	for _, line := range lines {
		id, ok := strings.CutPrefix(line.Path, "jobs/")
		if !ok || line.Final != http.StatusOK {
			continue
		}
		id, _, _ = strings.Cut(id, "/")
		jobID, err := strconv.ParseInt(id, 10, 64)
		Expect(err).NotTo(HaveOccurred())
		body, err := os.ReadFile(filepath.Join(recordings.Dir(runID, stage), fmt.Sprintf("attempt-%d", attempt), "logs", id+".txt"))
		if err == nil {
			logs[jobID] = body
		}
	}
	return logs
}

func storedArtifacts(runDir string, attempt int) []model.Artifact {
	GinkgoHelper()
	var listed []model.Artifact
	raw, err := os.ReadFile(filepath.Join(layout.AttemptDir(runDir, attempt), "artifacts.json"))
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Unmarshal(raw, &listed)).To(Succeed())
	return listed
}

func artifactIDs(artifacts []model.Artifact) []int64 {
	var out []int64
	for _, artifact := range artifacts {
		out = append(out, artifact.ID)
	}
	return out
}

func countKinds(kinds map[int64]model.JobKind) map[model.JobKind]int {
	counts := map[model.JobKind]int{}
	for _, kind := range kinds {
		counts[kind]++
	}
	return counts
}

// storedKinds reads each job's kind off disk: carried_forward when fetch.json
// names it, ran from a log or an expired or deleted tombstone, and
// not_applicable from its tombstone's reason.
func storedKinds(attemptDir string) map[int64]model.JobKind {
	GinkgoHelper()
	var fetch struct {
		CarriedForwardJobs []int64 `json:"carried_forward_jobs"`
	}
	raw, err := os.ReadFile(filepath.Join(attemptDir, "fetch.json"))
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Unmarshal(raw, &fetch)).To(Succeed())

	entries, err := os.ReadDir(filepath.Join(attemptDir, "jobs"))
	Expect(err).NotTo(HaveOccurred())
	kinds := map[int64]model.JobKind{}
	for _, entry := range entries {
		digits, _, _ := strings.Cut(entry.Name(), "_")
		id, err := strconv.ParseInt(digits, 10, 64)
		Expect(err).NotTo(HaveOccurred(), entry.Name())
		kinds[id] = storedKind(attemptDir, id, slices.Contains(fetch.CarriedForwardJobs, id))
	}
	return kinds
}

func storedKind(attemptDir string, id int64, carriedForward bool) model.JobKind {
	GinkgoHelper()
	job := jobDir(attemptDir, strconv.FormatInt(id, 10))
	_, logErr := os.Stat(filepath.Join(job, "log.txt"))
	tombstone, tombstoneErr := os.ReadFile(filepath.Join(job, "log.txt.tombstone"))
	switch {
	case carriedForward:
		Expect(logErr).To(HaveOccurred(), "carried-forward job %d has a log", id)
		Expect(tombstoneErr).To(HaveOccurred(), "carried-forward job %d has a tombstone", id)
		return model.CarriedForward
	case logErr == nil:
		Expect(tombstoneErr).To(HaveOccurred(), "job %d has both a log and a tombstone", id)
		return model.Ran
	}
	Expect(tombstoneErr).NotTo(HaveOccurred(), "job %d has neither a log nor a tombstone", id)
	var stone struct{ Reason string }
	Expect(json.Unmarshal(tombstone, &stone)).To(Succeed())
	if stone.Reason == string(model.NotApplicable) {
		return model.NotApplicable
	}
	Expect(stone.Reason).To(BeElementOf("expired", "deleted"), "job %d", id)
	return model.Ran
}
