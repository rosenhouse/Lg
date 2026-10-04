package mirror_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/version"
)

const (
	deletedRun = 37129738159
	ranJob     = "111221289888"
	ranJobLog  = "jobs/" + ranJob + "/logs"
	ranJobBlob = "/logs/" + ranJob + ".txt"
)

// cycleTimeout fails a spec whose Cycle hangs.
var cycleTimeout = SpecTimeout(20 * time.Second)

// attempt1Updated is attempt.json updated_at of run 37129390741 attempt 1.
var attempt1Updated = time.Date(2026, 10, 3, 14, 24, 12, 0, time.UTC)

// tombstonePath is the log tombstone of a job in a published attempt.
func tombstonePath(attemptDir, jobID string) string {
	GinkgoHelper()
	matches, err := filepath.Glob(filepath.Join(attemptDir, "jobs", jobID+"_*", "log.txt.tombstone"))
	Expect(err).NotTo(HaveOccurred())
	Expect(matches).To(HaveLen(1), "tombstone of job %s", jobID)
	Expect(filepath.Join(filepath.Dir(matches[0]), "log.txt")).NotTo(BeAnExistingFile())
	return matches[0]
}

func readTombstone(attemptDir, jobID string) map[string]any {
	GinkgoHelper()
	raw, err := os.ReadFile(tombstonePath(attemptDir, jobID))
	Expect(err).NotTo(HaveOccurred())
	var tombstone map[string]any
	Expect(json.Unmarshal(raw, &tombstone)).To(Succeed())
	return tombstone
}

var _ = DescribeTable("mirror.Cycle when a ran job's log 404s publishes no attempt within log_grace of the attempt's updated_at, and writes a deleted tombstone after it", Label("failures"),
	func(ctx SpecContext, host, match string) {
		env := harness.InProcess()
		// The run listing's updated_at is that of attempt 3, later than attempt 1's.
		Expect(env.Fake.Load(runID, "after-attempt-3")).To(Succeed())
		env.Fake.Fail(host, match, fakegithub.Fault{Status: http.StatusNotFound})

		env.Clock.Set(attempt1Updated.Add(time.Hour))
		Expect(env.Mirror.Cycle(ctx)).To(BeTransient())
		Expect(env.AttemptDirs(runID)).To(BeEmpty())
		Expect(env.Tombstones()).To(BeEmpty())

		env.Clock.Set(attempt1Updated.Add(time.Hour + time.Second))
		Expect(env.Mirror.Cycle(ctx)).To(Succeed())
		Expect(env.AttemptDirs(runID)).To(HaveLen(1))
		Expect(readTombstone(env.AttemptDirs(runID)[0], ranJob)).To(SatisfyAll(
			HaveKeyWithValue("reason", "deleted"),
			HaveKeyWithValue("http_status", BeEquivalentTo(404)),
			HaveKeyWithValue("url", env.Fake.URL()+"/repos/rosenhouse/lg/actions/"+ranJobLog),
			HaveKeyWithValue("tombstoned_at", "2026-10-03T15:24:13Z"),
		))
	},
	Entry("first-hop 404", "api", ranJobLog, cycleTimeout),
	Entry("blob 404", "blob", ranJobBlob, cycleTimeout),
)

var _ = Describe("mirror.Cycle when a log returns 410", Label("failures"), func() {
	It("writes an expired tombstone with http_status 410", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", ranJobLog, fakegithub.Fault{Status: http.StatusGone})

		Expect(env.Mirror.Cycle(ctx)).To(Succeed())
		Expect(env.AttemptDirs(runID)).To(HaveLen(1))
		Expect(readTombstone(env.AttemptDirs(runID)[0], ranJob)).To(SatisfyAll(
			HaveKeyWithValue("reason", "expired"),
			HaveKeyWithValue("http_status", BeEquivalentTo(410)),
		))
	}, cycleTimeout)

	It("keeps GitHub's message as rg can find it", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", ranJobLog, fakegithub.Fault{Status: http.StatusGone, Body: `{"message":"Logs for <job> & run expired"}`})

		Expect(env.Mirror.Cycle(ctx)).To(Succeed())
		Expect(os.ReadFile(tombstonePath(env.AttemptDirs(runID)[0], ranJob))).To(ContainSubstring(`"Logs for <job> & run expired"`))
	}, cycleTimeout)
})

var _ = DescribeTable("mirror.Cycle on a transient log failure publishes no attempt and writes no tombstone", Label("failures"),
	func(ctx SpecContext, host, match string, fault fakegithub.Fault) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail(host, match, fault)

		Expect(env.Mirror.Cycle(ctx)).To(BeTransient())
		Expect(env.AttemptDirs(runID)).To(BeEmpty())
		Expect(env.Tombstones()).To(BeEmpty())
		Expect(os.ReadDir(env.Tmp())).To(BeEmpty())
	},
	Entry("API 502", "api", ranJobLog, fakegithub.Fault{Status: http.StatusBadGateway}, cycleTimeout),
	Entry("blob 503", "blob", ranJobBlob, fakegithub.Fault{Status: http.StatusServiceUnavailable}, cycleTimeout),
	Entry("blob 403 AuthenticationFailed", "blob", ranJobBlob, fakegithub.Fault{Status: http.StatusForbidden}, cycleTimeout),
	Entry("blob 401", "blob", ranJobBlob, fakegithub.Fault{Status: http.StatusUnauthorized}, cycleTimeout),
	Entry("blob 429", "blob", ranJobBlob, fakegithub.Fault{Status: http.StatusTooManyRequests}, cycleTimeout),
	Entry("dropped connection", "api", ranJobLog, fakegithub.Fault{Drop: true}, cycleTimeout),
	Entry("body shorter than Content-Length", "blob", ranJobBlob, fakegithub.Fault{Truncate: true}, cycleTimeout),
	Entry("headers stalled past the header timeout", "api", ranJobLog, fakegithub.Fault{Stall: true}, cycleTimeout),
	Entry("body stalled past the idle timeout", "blob", ranJobBlob, fakegithub.Fault{Truncate: true, Stall: true}, cycleTimeout),
)

// bothRuns loads two runs and calls check once with each as the failing one,
// so that no spec depends on the order Cycle takes runs in.
func bothRuns(check func(env *harness.InProcessEnv, failing, other int64)) {
	GinkgoHelper()
	for _, pair := range [][2]int64{{runID, deletedRun}, {deletedRun, runID}} {
		By(fmt.Sprintf("failing run %d", pair[0]))
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		check(env, pair[0], pair[1])
	}
}

var _ = Describe("mirror.Cycle with a transient failure in one run", Label("failures"), func() {
	It("still publishes the other run and returns", func(ctx SpecContext) {
		bothRuns(func(env *harness.InProcessEnv, failing, other int64) {
			env.Fake.Fail("api", fmt.Sprintf("runs/%d/attempts/1/jobs", failing), fakegithub.Fault{Status: http.StatusBadGateway})

			Expect(env.Mirror.Cycle(ctx)).To(BeTransient())
			Expect(env.AttemptDirs(other)).To(HaveLen(1))
			Expect(env.AttemptDirs(failing)).To(BeEmpty())
		})
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when attempts/1 of a listed run returns 404", Label("failures"), func() {
	It("skips that run without a tombstone and publishes the others", func(ctx SpecContext) {
		bothRuns(func(env *harness.InProcessEnv, failing, other int64) {
			env.Fake.Fail("api", fmt.Sprintf("runs/%d/attempts/1", failing), fakegithub.Fault{Status: http.StatusNotFound})

			Expect(env.Mirror.Cycle(ctx)).To(Succeed())
			Expect(env.AttemptDirs(other)).To(HaveLen(1))
			Expect(env.AttemptDirs(failing)).To(BeEmpty())
			Expect(env.Tombstones()).To(HaveEach(Not(ContainSubstring(strconv.FormatInt(failing, 10)))))
		})
	}, cycleTimeout)
})

var _ = DescribeTable("mirror.Cycle with an unclassified failure in one run still publishes the other run and returns the failure, naming its run", Label("failures"),
	func(ctx SpecContext, host, match string, fault fakegithub.Fault, failure string) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		env.Fake.Fail(host, match, fault)

		err := env.Mirror.Cycle(ctx)
		Expect(err).To(MatchError(MatchRegexp(`^run 37129738159 attempt 1: http://\S+: %s$`, failure)))
		Expect(err).NotTo(BeTransient())
		Expect(env.AttemptDirs(runID)).To(HaveLen(1))
		Expect(env.AttemptDirs(deletedRun)).To(BeEmpty())
	},
	Entry("an API 400 on the attempt", "api", "runs/37129738159/attempts/1", fakegithub.Fault{Status: http.StatusBadRequest}, "400 Bad Request", cycleTimeout),
	Entry("an API 410 on the attempt", "api", "runs/37129738159/attempts/1", fakegithub.Fault{Status: http.StatusGone}, "410 Gone", cycleTimeout),
	Entry("an API 410 on the attempt's jobs", "api", "runs/37129738159/attempts/1/jobs", fakegithub.Fault{Status: http.StatusGone}, "410 Gone", cycleTimeout),
	Entry("an API 400 on a log", "api", "jobs/111222299886/logs", fakegithub.Fault{Status: http.StatusBadRequest}, "400 Bad Request", cycleTimeout),
	Entry("an unparsable 200 on the attempt", "api", "runs/37129738159/attempts/1", unparsable, "invalid character '<' looking for beginning of value", cycleTimeout),
	Entry("an unparsable 200 on the attempt's jobs", "api", "runs/37129738159/attempts/1/jobs", unparsable, "invalid character '<' looking for beginning of value", cycleTimeout),
	Entry("a jobs listing short of its total_count", "api", "runs/37129738159/attempts/1/jobs", fakegithub.Fault{Status: http.StatusOK, Body: `{"total_count":1,"jobs":[]}`}, "listed 0 of 1 jobs", cycleTimeout),
)

var unparsable = fakegithub.Fault{Status: http.StatusOK, Body: "<html>unicorn</html>"}

var _ = DescribeTable("mirror.Cycle when the API answers a status that blocks the cycle makes no further request", Label("failures"),
	func(ctx SpecContext, match string, status int) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		env.Fake.Fail("api", match, fakegithub.Fault{Status: status})

		Expect(env.Mirror.Cycle(ctx)).To(MatchError(ContainSubstring(fmt.Sprintf("%d %s", status, http.StatusText(status)))))
		requests := env.Fake.Requests()
		Expect(requests[len(requests)-1]).To(SatisfyAll(HaveField("Path", HaveSuffix(match)), HaveField("Status", status)))
		Expect(env.AttemptDirs(deletedRun)).To(BeEmpty())
	},
	Entry("401 on an attempt", "runs/37129738159/attempts/1", http.StatusUnauthorized, cycleTimeout),
	Entry("403 on an attempt", "runs/37129738159/attempts/1", http.StatusForbidden, cycleTimeout),
	Entry("429 on an attempt", "runs/37129738159/attempts/1", http.StatusTooManyRequests, cycleTimeout),
	Entry("403 on a log", "jobs/111222299886/logs", http.StatusForbidden, cycleTimeout),
)

var _ = Describe("attempt-N/fetch.json", Label("failures"), func() {
	It("records lg_format, lg_version, fetched_at, host, repo, run_id, attempt, run_created_at, run_attempt_at_fetch and sources with the API URL, status, bytes and sha256, and never a blob URL", func(ctx SpecContext) {
		env := harness.InProcess()
		// The run listing says attempt 3; attempt 1's own run_attempt says 1.
		Expect(env.Fake.Load(runID, "after-attempt-3")).To(Succeed())
		env.Fake.SetPageCap(5)
		env.Clock.Set(harness.DefaultNow().Add(500 * time.Millisecond))
		env.Mirror.Host = "ghe.corp.example"

		Expect(env.Mirror.Cycle(ctx)).To(Succeed())
		attempt1 := env.AttemptDirs(runID)[0]
		raw, err := os.ReadFile(filepath.Join(attempt1, "fetch.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("localhost"))
		var fetch struct {
			LgFormat          int    `json:"lg_format"`
			LgVersion         string `json:"lg_version"`
			FetchedAt         string `json:"fetched_at"`
			Host              string `json:"host"`
			Repo              string `json:"repo"`
			RunID             int64  `json:"run_id"`
			Attempt           int    `json:"attempt"`
			RunCreatedAt      string `json:"run_created_at"`
			RunAttemptAtFetch int    `json:"run_attempt_at_fetch"`
			Sources           map[string]struct {
				URL    string `json:"url"`
				Status int    `json:"status"`
				Bytes  int64  `json:"bytes"`
				SHA256 string `json:"sha256"`
			} `json:"sources"`
		}
		Expect(json.Unmarshal(raw, &fetch)).To(Succeed())
		var fields struct {
			Sources map[string]map[string]any `json:"sources"`
		}
		Expect(json.Unmarshal(raw, &fields)).To(Succeed())
		Expect(fetch.LgFormat).To(Equal(1))
		Expect(fetch.LgVersion).To(Equal(version.Version))
		Expect(fetch.FetchedAt).To(Equal("2026-10-03T18:00:00Z"))
		Expect(fetch.Host).To(Equal("ghe.corp.example"))
		Expect(fetch.Repo).To(Equal("rosenhouse/Lg"))
		Expect(fetch.RunID).To(BeEquivalentTo(runID))
		Expect(fetch.Attempt).To(Equal(1))
		Expect(fetch.RunCreatedAt).To(Equal("2026-10-03T14:22:54Z"))
		Expect(fetch.RunAttemptAtFetch).To(Equal(3))

		api := env.Fake.URL() + "/repos/rosenhouse/lg/actions/"
		urls := map[string]string{}
		for name, source := range fetch.Sources {
			urls[name] = source.URL
			if name == "jobs.json" {
				Expect(fields.Sources[name]).To(HaveKeyWithValue("pages", 3.0))
			} else {
				Expect(fields.Sources[name]).NotTo(HaveKey("pages"), name)
			}
			Expect(source.Status).To(Equal(http.StatusOK), name)
			stored, err := os.ReadFile(filepath.Join(attempt1, name))
			Expect(err).NotTo(HaveOccurred())
			Expect(source.Bytes).To(BeEquivalentTo(len(stored)), name)
			sum := sha256.Sum256(stored)
			Expect(source.SHA256).To(Equal(hex.EncodeToString(sum[:])), name)
		}
		Expect(urls).To(HaveKeyWithValue("attempt.json", api+"runs/37129390741/attempts/1"))
		Expect(urls).To(HaveKeyWithValue("jobs.json", api+"runs/37129390741/attempts/1/jobs?per_page=100"))
		logs, err := filepath.Glob(filepath.Join(attempt1, "jobs", "*", "log.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(logs).To(HaveLen(10))
		for _, log := range logs {
			name, err := filepath.Rel(attempt1, log)
			Expect(err).NotTo(HaveOccurred())
			jobID, _, _ := strings.Cut(filepath.Base(filepath.Dir(log)), "_")
			Expect(urls).To(HaveKeyWithValue(name, api+"jobs/"+jobID+"/logs"))
		}
		Expect(urls).To(HaveLen(12))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle with a job that has no steps and no runner", Label("failures"), func() {
	It("tombstones its log as not_applicable with the log's URL and the clock's time", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())

		Expect(env.Mirror.Cycle(ctx)).To(Succeed())
		Expect(readTombstone(env.AttemptDirs(runID)[0], "111221290616")).To(Equal(map[string]any{
			"lg_format":     1.0,
			"tombstoned_at": "2026-10-03T18:00:00Z",
			"target":        "log.txt",
			"url":           env.Fake.URL() + "/repos/rosenhouse/lg/actions/jobs/111221290616/logs",
			"http_status":   nil,
			"reason":        "not_applicable",
			"message":       "GitHub produces no log for a job with no steps and no runner",
		}))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when the jobs of a listed run's attempt return 404", Label("failures"), func() {
	It("skips that run and publishes the others", func(ctx SpecContext) {
		bothRuns(func(env *harness.InProcessEnv, failing, other int64) {
			env.Fake.Fail("api", fmt.Sprintf("runs/%d/attempts/1/jobs", failing), fakegithub.Fault{Status: http.StatusNotFound})

			Expect(env.Mirror.Cycle(ctx)).To(Succeed())
			Expect(env.AttemptDirs(other)).To(HaveLen(1))
			Expect(env.AttemptDirs(failing)).To(BeEmpty())
		})
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when an attempt has no updated_at", Label("failures"), func() {
	It("publishes nothing and writes no tombstone", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		env.Fake.Fail("api", "runs/37129738159/attempts/1", fakegithub.Fault{Status: http.StatusOK, Body: `{"status":"completed","run_attempt":1}`})

		Expect(env.Mirror.Cycle(ctx)).To(MatchError(ContainSubstring("no updated_at")))
		Expect(env.AttemptDirs(deletedRun)).To(BeEmpty())
		Expect(env.Tombstones()).To(BeEmpty())
	}, cycleTimeout)
})
