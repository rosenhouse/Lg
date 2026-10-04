package mirror_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

// readTombstone reads the log tombstone of a job in a published attempt.
func readTombstone(attemptDir, jobID string) map[string]any {
	GinkgoHelper()
	matches, err := filepath.Glob(filepath.Join(attemptDir, "jobs", jobID+"_*", "log.txt.tombstone"))
	Expect(err).NotTo(HaveOccurred())
	Expect(matches).To(HaveLen(1), "tombstone of job %s", jobID)
	Expect(filepath.Join(filepath.Dir(matches[0]), "log.txt")).NotTo(BeAnExistingFile())
	raw, err := os.ReadFile(matches[0])
	Expect(err).NotTo(HaveOccurred())
	var tombstone map[string]any
	Expect(json.Unmarshal(raw, &tombstone)).To(Succeed())
	return tombstone
}

var _ = DescribeTable("mirror.Cycle when a ran job's log 404s publishes no attempt within log_grace of the attempt's updated_at, and writes a deleted tombstone after it", Label("failures"),
	func(ctx SpecContext, host, match string) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
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
	Entry("dropped connection", "api", ranJobLog, fakegithub.Fault{Drop: true}, cycleTimeout),
	Entry("body shorter than Content-Length", "blob", ranJobBlob, fakegithub.Fault{Truncate: true}, cycleTimeout),
	Entry("headers stalled past the header timeout", "api", ranJobLog, fakegithub.Fault{Stall: true}, cycleTimeout),
	Entry("body stalled past the idle timeout", "blob", ranJobBlob, fakegithub.Fault{Truncate: true, Stall: true}, cycleTimeout),
)

var _ = Describe("mirror.Cycle with a transient failure in one run", Label("failures"), func() {
	It("still publishes the other run and returns", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		env.Fake.Fail("api", "runs/37129738159/attempts/1/jobs", fakegithub.Fault{Status: http.StatusBadGateway})

		Expect(env.Mirror.Cycle(ctx)).To(BeTransient())
		Expect(env.AttemptDirs(runID)).To(HaveLen(1))
		Expect(env.AttemptDirs(deletedRun)).To(BeEmpty())
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when attempts/1 of a listed run returns 404", Label("failures"), func() {
	It("skips that run without a tombstone and publishes the others", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		env.Fake.Fail("api", "runs/37129738159/attempts/1", fakegithub.Fault{Status: http.StatusNotFound})

		Expect(env.Mirror.Cycle(ctx)).To(Succeed())
		Expect(env.AttemptDirs(runID)).To(HaveLen(1))
		Expect(env.AttemptDirs(deletedRun)).To(BeEmpty())
		Expect(env.Tombstones()).To(HaveEach(Not(ContainSubstring("37129738159"))))
	}, cycleTimeout)
})

var _ = Describe("attempt-N/fetch.json", Label("failures"), func() {
	It("records lg_format, lg_version, fetched_at, host, repo, run_id, attempt, run_created_at, run_attempt_at_fetch and sources with the API URL, status, bytes and sha256, and never a blob URL", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())

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
		Expect(fetch.LgFormat).To(Equal(1))
		Expect(fetch.LgVersion).To(Equal(version.Version))
		Expect(fetch.FetchedAt).To(Equal("2026-10-03T18:00:00Z"))
		Expect(fetch.Host).To(Equal("github.com"))
		Expect(fetch.Repo).To(Equal("rosenhouse/Lg"))
		Expect(fetch.RunID).To(BeEquivalentTo(runID))
		Expect(fetch.Attempt).To(Equal(1))
		Expect(fetch.RunCreatedAt).To(Equal("2026-10-03T14:22:54Z"))
		Expect(fetch.RunAttemptAtFetch).To(Equal(1))

		api := env.Fake.URL() + "/repos/rosenhouse/lg/actions/"
		urls := map[string]string{}
		for name, source := range fetch.Sources {
			urls[name] = source.URL
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
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		env.Fake.Fail("api", "runs/37129738159/attempts/1/jobs", fakegithub.Fault{Status: http.StatusNotFound})

		Expect(env.Mirror.Cycle(ctx)).To(Succeed())
		Expect(env.AttemptDirs(runID)).To(HaveLen(1))
		Expect(env.AttemptDirs(deletedRun)).To(BeEmpty())
	}, cycleTimeout)
})
