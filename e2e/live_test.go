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
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
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
	thirdFixtureRun = int64(37129867594)
	expiringName    = "expires-in-1-day"
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

var _ = Describe("live", Label("live"), Ordered, func() {
	var (
		env            *harness.Env
		synced         *gexec.Session
		fixtureDir     string
		logsDeletedDir string
	)

	BeforeAll(func() {
		env = harness.NewLive(lgPath)
		window := liveWindow()
		env.WriteLiveConfig("backfill: "+window, "retention: "+window)

		synced = syncLive(env)
		fixtureDir = filepath.Join(env.Data(), fixtureRunDir)
		logsDeletedDir = runDir(env, logsDeletedRun)
	})

	// Ordered skips the specs after a failed one, so this one goes first.
	It("syncs rosenhouse/Lg and lg status reports no blocked state", func() {
		Expect(synced).To(gexec.Exit(0))
		session := env.Lg("status").Wait(harness.ExitTimeout)
		Expect(session).To(gexec.Exit(0))
		Expect(session.Out).To(gbytes.Say("blocked: no"))
		Expect(env.Status()).To(SatisfyAll(
			HaveKeyWithValue("blocked", BeNil()),
			HaveKeyWithValue("last_sync_errors", BeEmpty()),
			HaveKeyWithValue("repos", HaveKey("github.com/rosenhouse/lg")),
		))
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

	It("gives every job dir of runs 37129390741, 37129738159 and 37129867594 job.json plus exactly one of log.txt, log.txt.tombstone or an id in fetch.json carried_forward_jobs", func() {
		for _, runID := range []int64{fixtureRun, logsDeletedRun, thirdFixtureRun} {
			attempts, err := filepath.Glob(filepath.Join(runDir(env, runID), "attempt-*"))
			Expect(err).NotTo(HaveOccurred())
			Expect(attempts).NotTo(BeEmpty(), "run %d", runID)
			for _, attempt := range attempts {
				carriedForward := carriedForwardJobs(attempt)
				jobs, err := filepath.Glob(filepath.Join(attempt, "jobs", "*"))
				Expect(err).NotTo(HaveOccurred())
				Expect(jobs).NotTo(BeEmpty(), attempt)
				for _, job := range jobs {
					Expect(filepath.Join(job, "job.json")).To(BeARegularFile())
					id, ok := layout.DirID(filepath.Base(job))
					Expect(ok).To(BeTrue(), job)
					holds := 0
					for _, held := range []bool{
						exists(filepath.Join(job, "log.txt")),
						exists(filepath.Join(job, "log.txt.tombstone")),
						slices.Contains(carriedForward, id),
					} {
						if held {
							holds++
						}
					}
					Expect(holds).To(Equal(1), job)
				}
			}
		}
	})

	It("reports only the jobs flaky and timeout and their failing steps for lg flakes --sha 1a51097", func() {
		session := env.Lg("flakes", "--sha", "1a51097", "--json").Wait(harness.ExitTimeout)
		Expect(session).To(gexec.Exit(0))
		failingSteps := map[string][]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(session.Out.Contents())), "\n") {
			var finding struct {
				Kind         string
				RunID        int64 `json:"run_id"`
				Job          string
				Step         *string
				FailingSteps []string `json:"failing_steps"`
			}
			Expect(json.Unmarshal([]byte(line), &finding)).To(Succeed(), line)
			Expect(finding.Kind).To(Equal("rerun"), line)
			Expect(finding.RunID).To(Equal(int64(fixtureRun)), line)
			key := finding.Job
			if finding.Step != nil {
				key += " / " + *finding.Step
			}
			failingSteps[key] = finding.FailingSteps
		}
		Expect(failingSteps).To(Equal(map[string][]string{
			"flaky":                              {"Fail on first attempt only"},
			"flaky / Fail on first attempt only": {},
			"timeout":                            {"Time out on first attempt only"},
			"timeout / Time out on first attempt only": {},
		}))
	})

	It("decodes an LG_MARKER hit from `lg paths` + grep with lg where", func() {
		session := env.Bash("lg paths --sha 1a51097 -0 | xargs -0 -r rg --no-config -Hn LG_MARKER | lg where").Wait(harness.ExitTimeout)
		Expect(session).To(gexec.Exit(0))
		var decoded []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(string(session.Out.Contents())), "\n") {
			var hit map[string]any
			Expect(json.Unmarshal([]byte(line), &hit)).To(Succeed(), line)
			Expect(hit).To(HaveKeyWithValue("run_id", BeEquivalentTo(fixtureRun)))
			Expect(hit).To(HaveKeyWithValue("text", ContainSubstring("LG_MARKER")))
			Expect(hit).To(HaveKeyWithValue("line", BeNumerically(">", 0)))
			Expect(lineOf(hit["path"].(string), int(hit["line"].(float64)))).To(Equal(hit["text"]))
			decoded = append(decoded, hit)
		}
		Expect(decoded).To(ContainElement(SatisfyAll(
			HaveKeyWithValue("attempt", BeEquivalentTo(1)),
			HaveKeyWithValue("job", "flaky"),
			HaveKeyWithValue("job_conclusion", "failure"),
			HaveKeyWithValue("sha", HavePrefix("1a51097")),
			HaveKeyWithValue("text", HaveSuffix("LG_MARKER flaky failure attempt=1")),
			HaveKeyWithValue("html_url", "https://github.com/rosenhouse/Lg/actions/runs/37129390741/job/111221289888"),
		)))
	})

	It("handles the expires-in-1-day artifacts the way R1 recorded", func() {
		By("finding them delisted")
		for _, runID := range []int64{fixtureRun, logsDeletedRun, thirdFixtureRun} {
			dir := runDir(env, runID)
			Expect(filepath.Glob(filepath.Join(dir, "artifacts", "*_"+expiringName))).To(BeEmpty(), "run %d", runID)
			snapshots, err := filepath.Glob(filepath.Join(dir, "attempt-*", "artifacts.json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(snapshots).NotTo(BeEmpty(), "run %d", runID)
			for _, snapshot := range snapshots {
				Expect(os.ReadFile(snapshot)).NotTo(ContainSubstring(expiringName), snapshot)
			}
		}

		By("retrying them in a store that listed them before they expired")
		early := harness.NewLive(lgPath)
		realGH := early.Getenv("LG_GH")
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		Expect(fake.Load(logsDeletedRun, "logs-deleted")).To(Succeed())
		listed := map[int64][]model.Artifact{
			fixtureRun:     expiring(fixtureRun, "after-attempt-1"),
			logsDeletedRun: expiring(logsDeletedRun, "logs-deleted"),
		}
		for _, artifacts := range listed {
			Expect(artifacts).To(HaveLen(1))
			fake.Fail("blob", fmt.Sprintf("/artifacts/%d.zip", artifacts[0].ID), fakegithub.Fault{Status: http.StatusServiceUnavailable})
		}
		early.Setenv("LG_GH", fakegh.New(GinkgoT().TempDir()).Path)
		early.Setenv("LG_TEST_NOW", harness.DefaultNow().Format(time.RFC3339))
		window := liveWindow()
		early.WriteConfig(fake.URL(), "backfill: "+window, "retention: "+window)
		Expect(early.Sync()).To(gexec.Exit(1))

		early.Setenv("LG_GH", realGH)
		early.Setenv("LG_TEST_NOW", "")
		early.WriteLiveConfig("backfill: "+window, "retention: "+window)
		Expect(syncLive(early)).To(gexec.Exit(0))

		for runID, artifacts := range listed {
			artifact := artifacts[0]
			status := recordedZipStatus(runID, artifact.ID)
			reason := map[int]string{http.StatusGone: "expired", http.StatusNotFound: "deleted"}[status]
			Expect(reason).NotTo(BeEmpty(), "R1 recorded %d for artifact %d", status, artifact.ID)
			tombstone := filepath.Join(layout.ArtifactDir(runDir(early, runID), artifact.ID, artifact.Name), "artifact.zip.tombstone")
			Expect(readJSON(tombstone)).To(SatisfyAll(
				HaveKeyWithValue("reason", reason),
				HaveKeyWithValue("http_status", BeEquivalentTo(status)),
			), "artifact %d", artifact.ID)
		}
	})
})

// expiring gives the expires-in-1-day artifacts of a recorded listing.
func expiring(runID int64, stage string) []model.Artifact {
	GinkgoHelper()
	artifacts, err := recordings.Artifacts(runID, stage)
	Expect(err).NotTo(HaveOccurred())
	return slices.DeleteFunc(artifacts, func(a model.Artifact) bool { return a.Name != expiringName })
}

// recordedZipStatus is the final status R1 recorded for an artifact's zip.
func recordedZipStatus(runID, artifactID int64) int {
	GinkgoHelper()
	status, err := os.Open(filepath.Join(recordings.Dir(runID, "after-expiry"), "status.txt"))
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = status.Close() }()
	lines, err := recordings.ParseStatus(status)
	Expect(err).NotTo(HaveOccurred())
	path := fmt.Sprintf("artifacts/%d/zip", artifactID)
	i := slices.IndexFunc(lines, func(l recordings.Line) bool { return l.Path == path })
	Expect(i).NotTo(BeNumerically("<", 0), path)
	return lines[i].Final
}

func carriedForwardJobs(attemptDir string) []int64 {
	GinkgoHelper()
	var fetch struct {
		CarriedForwardJobs []int64 `json:"carried_forward_jobs"`
	}
	raw, err := os.ReadFile(filepath.Join(attemptDir, "fetch.json"))
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Unmarshal(raw, &fetch)).To(Succeed())
	return fetch.CarriedForwardJobs
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// lineOf gives line n of the file at path, without its newline.
func lineOf(path string, n int) string {
	GinkgoHelper()
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	lines := strings.Split(string(raw), "\n")
	Expect(n).To(BeNumerically("<=", len(lines)), path)
	return lines[n-1]
}

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
// sync, and the next sync resumes where it stopped. It gives the last sync.
func syncLive(env *harness.Env) *gexec.Session {
	var session *gexec.Session
	for range 3 {
		if session = env.Lg("sync").Wait(liveSyncTimeout); session.ExitCode() == 0 {
			break
		}
	}
	return session
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
	carriedForward := carriedForwardJobs(attemptDir)
	entries, err := os.ReadDir(filepath.Join(attemptDir, "jobs"))
	Expect(err).NotTo(HaveOccurred())
	kinds := map[int64]model.JobKind{}
	for _, entry := range entries {
		digits, _, _ := strings.Cut(entry.Name(), "_")
		id, err := strconv.ParseInt(digits, 10, 64)
		Expect(err).NotTo(HaveOccurred(), entry.Name())
		kinds[id] = storedKind(attemptDir, id, slices.Contains(carriedForward, id))
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
