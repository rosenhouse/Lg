package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

// liveSyncTimeout bounds one sync of every run github.com lists for rosenhouse/Lg.
const liveSyncTimeout = 15 * time.Minute

const (
	fixtureStage     = "after-attempt-3"
	logsDeletedStage = "after-expiry"
)

var _ = Describe("lg sync against github.com/rosenhouse/Lg", Label("live"), Ordered, func() {
	var (
		env            *harness.Env
		fixtureDir     string
		logsDeletedDir string
	)

	BeforeAll(func() {
		env = harness.NewLive(lgPath)
		env.WriteLiveConfig("backfill: 90d", "retention: 90d")

		Eventually(env.Lg("sync"), liveSyncTimeout).Should(gexec.Exit(0))
		fixtureDir = filepath.Join(env.Data(), fixtureRunDir)
		logsDeletedDir = runDir(env, logsDeletedRun)
	})

	It("mirrors run 37129390741 with attempts 1–3, the recorded job ids and the recorded job kinds", func() {
		for attempt := 1; attempt <= 3; attempt++ {
			dir := layout.AttemptDir(fixtureDir, attempt)
			Expect(dir).To(BeADirectory())
			kinds := recordedKinds(fixtureRun, fixtureStage, attempt)
			Expect(storedJobIDs(dir)).To(ConsistOf(ids(kinds)), "attempt %d", attempt)
			for id, kind := range kinds {
				Expect(storedKind(dir, id)).To(Equal(kind), "attempt %d job %d", attempt, id)
			}
		}
	})

	It("stores each log byte-identical to the recording, or a tombstone with reason expired or deleted", func() {
		for attempt := 1; attempt <= 3; attempt++ {
			dir := layout.AttemptDir(fixtureDir, attempt)
			logs := recordedLogs(fixtureRun, fixtureStage, attempt)
			Expect(logs).NotTo(BeEmpty())
			for id, want := range logs {
				Expect(recordings.CompareLog(want, jobDir(dir, strconv.FormatInt(id, 10)))).To(Succeed(), "attempt %d job %d", attempt, id)
			}
		}
	})

	It("writes deleted tombstones for the 10 ran jobs of run 37129738159", func() {
		dir := layout.AttemptDir(logsDeletedDir, 1)
		ran := 0
		for id, kind := range recordedKinds(logsDeletedRun, logsDeletedStage, 1) {
			if kind != model.Ran {
				continue
			}
			ran++
			job := jobDir(dir, strconv.FormatInt(id, 10))
			Expect(filepath.Join(job, "log.txt")).NotTo(BeAnExistingFile())
			Expect(readJSON(filepath.Join(job, "log.txt.tombstone"))).To(HaveKeyWithValue("reason", "deleted"), "job %d", id)
		}
		Expect(ran).To(Equal(10))
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
		for attempt := 1; attempt <= 3; attempt++ {
			compare(fixtureDir, fixtureRun, fixtureStage, attempt)
		}
		compare(logsDeletedDir, logsDeletedRun, logsDeletedStage, 1)
	})

	It("writes a zip, or an expired or deleted tombstone, for every artifact listed for run 37129390741", func() {
		var listed []model.Artifact
		raw, err := os.ReadFile(filepath.Join(layout.AttemptDir(fixtureDir, 3), "artifacts.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(raw, &listed)).To(Succeed())
		Expect(listed).NotTo(BeEmpty())

		for _, artifact := range listed {
			dir := layout.ArtifactDir(fixtureDir, artifact.ID, artifact.Name)
			zip, err := os.ReadFile(filepath.Join(dir, "artifact.zip"))
			if err == nil {
				recorded := filepath.Join(recordings.Dir(fixtureRun, fixtureStage), "artifacts", fmt.Sprintf("%d.zip", artifact.ID))
				if _, err := os.Stat(recorded); err == nil {
					Expect(os.ReadFile(recorded)).To(Equal(zip), "artifact %d", artifact.ID)
				}
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
			Expect(body).To(Equal(compact.Bytes()), base)
		}
	})
})

func get(url string) []byte {
	GinkgoHelper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("User-Agent", "lg/test")
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
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

// recordedKinds classifies a recorded attempt's jobs by id.
func recordedKinds(runID int64, stage string, attempt int) map[int64]model.JobKind {
	GinkgoHelper()
	run, err := recordings.Attempt(runID, stage, attempt)
	Expect(err).NotTo(HaveOccurred())
	jobs, err := recordings.Jobs(runID, stage, attempt)
	Expect(err).NotTo(HaveOccurred())
	kinds := map[int64]model.JobKind{}
	for _, job := range jobs {
		kinds[job.ID] = model.Classify(job, run.RunStartedAt)
	}
	return kinds
}

// recordedLogs are the log bodies GitHub served for a recorded attempt, by job id.
func recordedLogs(runID int64, stage string, attempt int) map[int64][]byte {
	GinkgoHelper()
	status, err := os.Open(filepath.Join(recordings.Dir(runID, stage), "status.txt"))
	Expect(err).NotTo(HaveOccurred())
	defer status.Close()
	lines, err := recordings.ParseStatus(status)
	Expect(err).NotTo(HaveOccurred())
	logs := map[int64][]byte{}
	for _, line := range lines {
		id, ok := strings.CutPrefix(line.Path, "jobs/")
		id, _, _ = strings.Cut(id, "/")
		if !ok || line.Final != http.StatusOK {
			continue
		}
		jobID, err := strconv.ParseInt(id, 10, 64)
		Expect(err).NotTo(HaveOccurred())
		body, err := os.ReadFile(filepath.Join(recordings.Dir(runID, stage), fmt.Sprintf("attempt-%d", attempt), "logs", id+".txt"))
		if err == nil {
			logs[jobID] = body
		}
	}
	return logs
}

func ids(kinds map[int64]model.JobKind) []int64 {
	var out []int64
	for id := range kinds {
		out = append(out, id)
	}
	return out
}

func storedJobIDs(attemptDir string) []int64 {
	GinkgoHelper()
	entries, err := os.ReadDir(filepath.Join(attemptDir, "jobs"))
	Expect(err).NotTo(HaveOccurred())
	var out []int64
	for _, entry := range entries {
		digits, _, _ := strings.Cut(entry.Name(), "_")
		id, err := strconv.ParseInt(digits, 10, 64)
		Expect(err).NotTo(HaveOccurred(), entry.Name())
		out = append(out, id)
	}
	return out
}

// storedKind reads a job's kind off disk: carried_forward when fetch.json
// names it, ran from a log or an expired or deleted tombstone, and
// not_applicable from its tombstone's reason.
func storedKind(attemptDir string, id int64) model.JobKind {
	GinkgoHelper()
	job := jobDir(attemptDir, strconv.FormatInt(id, 10))
	_, logErr := os.Stat(filepath.Join(job, "log.txt"))
	tombstone, tombstoneErr := os.ReadFile(filepath.Join(job, "log.txt.tombstone"))
	var fetch struct {
		CarriedForwardJobs []int64 `json:"carried_forward_jobs"`
	}
	raw, err := os.ReadFile(filepath.Join(attemptDir, "fetch.json"))
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Unmarshal(raw, &fetch)).To(Succeed())
	switch {
	case slices.Contains(fetch.CarriedForwardJobs, id):
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
