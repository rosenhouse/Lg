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
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

// liveSyncTimeout bounds one sync of every run github.com lists for rosenhouse/Lg.
const liveSyncTimeout = 15 * time.Minute

// maxLiveRuns is how many runs one sync fetches within liveSyncTimeout, at
// about 3.4 s each.
const maxLiveRuns = 200

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

var _ = Describe("live", Label("live"), Ordered, ContinueOnFailure, func() {
	var (
		env        *harness.Env
		fixtureDir string
	)

	BeforeAll(func() {
		env = harness.NewLive(lgPath)
		env.WriteLiveConfig(windowConfig()...)

		Expect(syncLive(env)).To(gexec.Exit(0))
		fixtureDir = filepath.Join(env.Data(), fixtureRunDir)
	})

	It("syncs rosenhouse/Lg and lg status reports no blocked state", func() {
		session := env.Lg("status").Wait(harness.ExitTimeout)
		Expect(session).To(gexec.Exit(0))
		Expect(session.Out).To(gbytes.Say(`(?m)^blocked: no$`))
		Expect(string(session.Err.Contents())).NotTo(ContainSubstring("lg: warning"))
		Expect(env.Status()).To(SatisfyAll(
			HaveKeyWithValue("blocked", BeNil()),
			HaveKeyWithValue("last_sync_ok_at", Not(BeNil())),
			HaveKeyWithValue("last_sync_errors", BeEmpty()),
			HaveKeyWithValue("repos", HaveKey("github.com/rosenhouse/lg")),
		))
	})

	Describe("lg sync against github.com/rosenhouse/Lg", func() {
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
			dir := layout.AttemptDir(runDir(env, logsDeletedRun), 1)
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
			compare(runDir(env, logsDeletedRun), logsDeletedRun, logsDeletedStage, 1)
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

	It("gives every job dir of runs 37129390741, 37129738159 and 37129867594 job.json plus exactly one of log.txt, log.txt.tombstone or an id in fetch.json carried_forward_jobs", func() {
		for _, runID := range []int64{fixtureRun, logsDeletedRun, thirdFixtureRun} {
			for _, attempt := range glob(runDir(env, runID), "attempt-*") {
				carried := carriedForwardJobs(attempt)
				jobs := glob(attempt, "jobs", "*")
				Expect(jobs).NotTo(BeEmpty(), attempt)
				for _, job := range jobs {
					Expect(filepath.Join(job, "job.json")).To(BeARegularFile())
					present := 0
					for _, has := range []bool{
						exists(filepath.Join(job, "log.txt")),
						exists(filepath.Join(job, "log.txt.tombstone")),
						slices.Contains(carried, jobIDOf(filepath.Base(job))),
					} {
						if has {
							present++
						}
					}
					Expect(present).To(Equal(1), job)
				}
			}
		}
	})

	It("reports only the jobs flaky and timeout and their failing steps for lg flakes --sha 1a51097", func() {
		session := env.Lg("flakes", "--sha", "1a51097", "--json").Wait(harness.ExitTimeout)
		Expect(session).To(gexec.Exit(0))
		failingSteps := map[string][]string{}
		for _, line := range outputLines(session) {
			var finding struct {
				Kind         string   `json:"kind"`
				RunID        int64    `json:"run_id"`
				Job          string   `json:"job"`
				Step         *string  `json:"step"`
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
		hits := decoded(session)
		for _, hit := range hits {
			Expect(hit).To(HaveKeyWithValue("run_id", BeEquivalentTo(fixtureRun)))
			Expect(hit).To(HaveKeyWithValue("text", ContainSubstring("LG_MARKER")))
			Expect(hit).To(HaveKeyWithValue("line", BeNumerically(">", 0)))
			Expect(lineOf(hit["path"].(string), int(hit["line"].(float64)))).To(Equal(hit["text"]))
		}
		Expect(hits).To(ContainElement(SatisfyAll(
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
			for _, snapshot := range glob(dir, "attempt-*", "artifacts.json") {
				Expect(os.ReadFile(snapshot)).NotTo(ContainSubstring(expiringName), snapshot)
			}
		}

		By("retrying them in a store that listed them before they expired")
		// A rerun deleted 11275917910 before it expired.
		recorded := []struct {
			runID, artifactID int64
			reason            string
			status            int
		}{
			{fixtureRun, 11275917910, "deleted", http.StatusNotFound},
			{fixtureRun, expiredArtifact, "expired", http.StatusGone},
			{logsDeletedRun, 11276237903, "expired", http.StatusGone},
		}
		live := harness.NewLive(lgPath)
		seed := harness.New(lgPath)
		seed.Setenv("LG_HOME", live.Getenv("LG_HOME"))
		fake := fakegithub.Start(fixtureRun, "after-attempt-1")
		Expect(fake.Load(logsDeletedRun, "logs-deleted")).To(Succeed())
		// A failed download leaves each one pending until the live sync.
		for _, r := range recorded {
			fake.Fail("api", fmt.Sprintf("/artifacts/%d/zip", r.artifactID), fakegithub.Fault{Status: http.StatusServiceUnavailable})
		}
		seed.WriteConfig(fake.URL(), windowConfig()...)
		Expect(seed.Sync()).To(gexec.Exit(1))
		// Attempt 3 lists 11276327411.
		Expect(fake.Advance(fixtureRun, fixtureStage)).To(Succeed())
		Expect(seed.Sync()).To(gexec.Exit(1))

		live.WriteLiveConfig(windowConfig()...)
		Expect(syncLive(live)).To(gexec.Exit(0))

		for _, r := range recorded {
			tombstone := filepath.Join(layout.ArtifactDir(runDir(live, r.runID), r.artifactID, expiringName), "artifact.zip.tombstone")
			Expect(readJSON(tombstone)).To(SatisfyAll(
				HaveKeyWithValue("reason", r.reason),
				HaveKeyWithValue("http_status", BeEquivalentTo(r.status)),
				HaveKeyWithValue("url", HavePrefix("https://api.github.com/")),
			), "artifact %d", r.artifactID)
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

// windowConfig gives the backfill and retention lines of liveWindowDays.
func windowConfig() []string {
	GinkgoHelper()
	window := fmt.Sprintf("%dd", liveWindowDays())
	return []string{"backfill: " + window, "retention: " + window}
}

// liveWindowDays is 90, or more once a fixture run is older, so the fixture
// runs stay in backfill and retention as they age.
func liveWindowDays() int {
	GinkgoHelper()
	days := 90
	now := clock.Real{}.Now()
	for runID, stage := range map[int64]string{fixtureRun: fixtureStage, logsDeletedRun: logsDeletedStage} {
		run, err := recordings.Attempt(runID, stage, 1)
		Expect(err).NotTo(HaveOccurred())
		days = max(days, int(now.Sub(run.CreatedAt).Hours()/24)+2)
	}
	return days
}

// expectSyncableWindow fails when github.com lists more runs in the live
// window than one sync fetches within liveSyncTimeout.
func expectSyncableWindow() {
	GinkgoHelper()
	since := clock.Real{}.Now().AddDate(0, 0, -liveWindowDays()).Format(time.DateOnly)
	var listing struct {
		TotalCount int `json:"total_count"`
	}
	Expect(json.Unmarshal(get("https://api.github.com/repos/rosenhouse/Lg/actions/runs?per_page=1&created=%3E%3D"+since), &listing)).To(Succeed())
	Expect(listing.TotalCount).To(BeNumerically("<=", maxLiveRuns),
		"%d runs since %s; re-record the fixture runs to shrink the window", listing.TotalCount, since)
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

// syncLive retries a sync that exits 1, because one transient GitHub error on
// any run fails a sync, and the next sync resumes where it stopped. It waits
// before each retry, since a transient error can outlast a quick one. It
// asserts each sync left data/ append-only, and gives the last sync.
func syncLive(env *harness.Env) *gexec.Session {
	GinkgoHelper()
	expectSyncableWindow()
	var session *gexec.Session
	for _, wait := range []time.Duration{30 * time.Second, time.Minute, 0} {
		before := treesnap.Snapshot(env.Data())
		session = env.Lg("sync").Wait(liveSyncTimeout)
		Expect(treesnap.Snapshot(env.Data())).To(treesnap.BeAppendOnlyFrom(before))
		if session.ExitCode() != 1 || wait == 0 {
			break
		}
		GinkgoWriter.Printf("live sync exited 1: %s\nretrying in %s\n", session.Err.Contents(), wait)
		<-clock.Real{}.After(wait)
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
	carried := carriedForwardJobs(attemptDir)
	entries, err := os.ReadDir(filepath.Join(attemptDir, "jobs"))
	Expect(err).NotTo(HaveOccurred())
	kinds := map[int64]model.JobKind{}
	for _, entry := range entries {
		id := jobIDOf(entry.Name())
		kinds[id] = storedKind(attemptDir, id, slices.Contains(carried, id))
	}
	return kinds
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

// jobIDOf gives the id of a job dir named <id>_<slug>.
func jobIDOf(name string) int64 {
	GinkgoHelper()
	digits, _, _ := strings.Cut(name, "_")
	id, err := strconv.ParseInt(digits, 10, 64)
	Expect(err).NotTo(HaveOccurred(), name)
	return id
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

// lineOf gives line n of the file at path, without its newline.
func lineOf(path string, n int) string {
	GinkgoHelper()
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	lines := strings.Split(string(raw), "\n")
	Expect(n).To(BeNumerically("<=", len(lines)), path)
	return lines[n-1]
}
