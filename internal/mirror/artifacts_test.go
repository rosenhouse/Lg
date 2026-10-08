package mirror_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

const (
	flakyReport     = "11276401837"
	flakyReportBlob = "/artifacts/" + flakyReport + ".zip"
	passArtifact    = "11276272069"
	expiresIn1Day   = "11275917910"
)

// expiresIn1DayAt is expires_at of artifact 11275917910, and expiresIn1DayCreatedAt its created_at.
var (
	expiresIn1DayAt        = time.Date(2026, 10, 4, 14, 23, 1, 0, time.UTC)
	expiresIn1DayCreatedAt = time.Date(2026, 10, 3, 14, 23, 2, 0, time.UTC)
)

// artifactDir is the published dir of a run's artifact.
func artifactDir(env *harness.InProcessEnv, runID int64, artifactID string) string {
	GinkgoHelper()
	var dir string
	Expect(env.ArtifactDirs(runID)).To(ContainElement(ContainSubstring("/artifacts/"+artifactID+"_"), &dir))
	return dir
}

func readZipTombstone(env *harness.InProcessEnv, runID int64, artifactID string) map[string]any {
	GinkgoHelper()
	dir := artifactDir(env, runID, artifactID)
	Expect(filepath.Join(dir, "artifact.json")).To(BeARegularFile())
	Expect(filepath.Join(dir, "artifact.zip")).NotTo(BeAnExistingFile())
	return readJSONFile(filepath.Join(dir, "artifact.zip.tombstone"))
}

func zipURL(env *harness.InProcessEnv, artifactID string) string {
	return env.Fake.URL() + "/repos/rosenhouse/lg/actions/artifacts/" + artifactID + "/zip"
}

func requestedZip(env *harness.InProcessEnv, artifactID string) bool {
	for _, r := range env.Fake.Requests() {
		if strings.HasSuffix(r.Path, "/artifacts/"+artifactID+"/zip") {
			return true
		}
	}
	return false
}

var _ = Describe("mirror.Cycle over after-attempt-1 and logs-deleted together", Label("artifacts"), func() {
	It("requests no job log before the last artifact zip, and downloads the newer run's artifacts first", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		var zipsOf []string
		lastZip, firstLog := -1, -1
		for i, r := range env.Fake.Requests() {
			switch {
			case strings.HasSuffix(r.Path, "/zip"):
				lastZip = i
				zipsOf = append(zipsOf, r.Path)
			case strings.Contains(r.Path, "/jobs/") && strings.HasSuffix(r.Path, "/logs") && firstLog < 0:
				firstLog = i
			}
		}
		Expect(zipsOf).To(HaveExactElements(
			HaveSuffix("/artifacts/11276546973/zip"),
			HaveSuffix("/artifacts/11276517061/zip"),
			HaveSuffix("/artifacts/11276237903/zip"),
			HaveSuffix("/artifacts/11276128157/zip"),
			HaveSuffix("/artifacts/11276401837/zip"),
			HaveSuffix("/artifacts/11276272069/zip"),
			HaveSuffix("/artifacts/11276182467/zip"),
			HaveSuffix("/artifacts/11275917910/zip"),
		))
		Expect(firstLog).To(BeNumerically(">", lastZip))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle for a run still in progress", Label("artifacts"), func() {
	It("downloads its listed artifacts and publishes no attempt", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(scenario.InProgress(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), cloneID), 1))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(env.ArtifactDirs(cloneID)).To(SatisfyAll(
			HaveLen(4),
			HaveEach(WithTransform(func(dir string) string { return filepath.Join(dir, "artifact.zip") }, BeARegularFile())),
		))
		Expect(env.AttemptDirs(cloneID)).To(BeEmpty())
		raw, err := os.ReadFile(filepath.Join(env.ArtifactDirs(cloneID)[0], "fetch.json"))
		Expect(err).NotTo(HaveOccurred())
		var fetch map[string]any
		Expect(json.Unmarshal(raw, &fetch)).To(Succeed())
		Expect(fetch).To(SatisfyAll(
			HaveKeyWithValue("run_status_at_fetch", "in_progress"),
			HaveKeyWithValue("run_attempt_at_fetch", 1.0),
		))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle with artifact_max_bytes 700", Label("artifacts"), func() {
	It("writes a too_large tombstone for pass-artifact 11276272069 (751 B) without requesting its zip, with size_in_bytes in the message", func(ctx SpecContext) {
		env := harness.InProcess()
		env.Mirror.ArtifactMaxBytes = 700
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readZipTombstone(env, runID, passArtifact)).To(SatisfyAll(
			HaveKeyWithValue("reason", "too_large"),
			HaveKeyWithValue("http_status", BeNil()),
			HaveKeyWithValue("message", ContainSubstring("751")),
			HaveKeyWithValue("url", zipURL(env, passArtifact)),
		))
		Expect(requestedZip(env, passArtifact)).To(BeFalse())
		Expect(filepath.Join(artifactDir(env, runID, flakyReport), "artifact.zip")).To(BeARegularFile())
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle", Label("artifacts"), func() {
	It("writes a too_large tombstone when a zip stream exceeds artifact_max_bytes although size_in_bytes does not", func(ctx SpecContext) {
		env := harness.InProcess()
		env.Mirror.ArtifactMaxBytes = 700
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("blob", flakyReportBlob, fakegithub.Fault{Status: http.StatusOK, Body: strings.Repeat("x", 701)})

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readZipTombstone(env, runID, flakyReport)).To(SatisfyAll(
			HaveKeyWithValue("reason", "too_large"),
			HaveKeyWithValue("message", ContainSubstring("700")),
			HaveKeyWithValue("url", zipURL(env, flakyReport)),
		))
		Expect(env.Tmp()).To(BeSwept())
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when the listing says expired: true, as GitHub's docs allow but no recording shows", Label("artifacts"), func() {
	It("writes an expired tombstone without a request", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(scenario.Expire(scenario.Recorded(runID, "after-attempt-1"), 11275917910))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readZipTombstone(env, runID, expiresIn1Day)).To(SatisfyAll(
			HaveKeyWithValue("reason", "expired"),
			HaveKeyWithValue("http_status", BeNil()),
			HaveKeyWithValue("url", zipURL(env, expiresIn1Day)),
		))
		Expect(requestedZip(env, expiresIn1Day)).To(BeFalse())
	}, cycleTimeout)
})

var _ = DescribeTable("mirror.Cycle when a zip fails permanently writes artifact.json plus a tombstone", Label("artifacts"),
	func(ctx SpecContext, status int, now time.Time, reason string) {
		env := harness.InProcess()
		env.Clock.Set(now)
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", "artifacts/"+expiresIn1Day+"/zip", fakegithub.Fault{Status: status})

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readZipTombstone(env, runID, expiresIn1Day)).To(SatisfyAll(
			HaveKeyWithValue("reason", reason),
			HaveKeyWithValue("http_status", BeEquivalentTo(status)),
			HaveKeyWithValue("url", zipURL(env, expiresIn1Day)),
		))
	},
	Entry("410: expired", http.StatusGone, harness.DefaultNow(), "expired", cycleTimeout),
	Entry("404 with expires_at in the past: deleted, as recorded for 11275917910 at after-expiry", http.StatusNotFound, expiresIn1DayAt.Add(time.Hour), "deleted", cycleTimeout),
	Entry("404 with expires_at in the future: deleted", http.StatusNotFound, harness.DefaultNow(), "deleted", cycleTimeout),
)

var _ = Describe("mirror.Cycle when a zip 404s at the blob hop", Label("artifacts"), func() {
	It("keeps the artifact pending within log_grace of its created_at, and tombstones it as deleted after", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("blob", "/artifacts/"+expiresIn1Day+".zip", fakegithub.Fault{Status: http.StatusNotFound})
		env.Clock.Set(expiresIn1DayCreatedAt.Add(env.Mirror.LogGrace))

		Expect(env.Sync(ctx)).To(BeTransient())
		Expect(env.ArtifactDirs(runID)).To(HaveLen(3))
		Expect(env.ArtifactDirs(runID)).NotTo(ContainElement(ContainSubstring("/" + expiresIn1Day + "_")))
		Expect(readPending(env)).To(HaveKeyWithValue("github.com/37129390741", HaveLen(1)))

		env.Clock.Set(expiresIn1DayCreatedAt.Add(env.Mirror.LogGrace + time.Second))
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readZipTombstone(env, runID, expiresIn1Day)).To(SatisfyAll(
			HaveKeyWithValue("reason", "deleted"),
			HaveKeyWithValue("http_status", BeEquivalentTo(http.StatusNotFound)),
		))
		Expect(readPending(env)).To(BeEmpty())
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when a zip does not match its digest", Label("artifacts"), func() {
	It("publishes no artifact dir, and the next cycle downloads it", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("blob", flakyReportBlob, fakegithub.Fault{Status: http.StatusOK, Body: "not the recorded zip", Times: 1})

		Expect(env.Sync(ctx)).To(BeTransient())
		Expect(env.ArtifactDirs(runID)).To(HaveLen(3))
		Expect(env.ArtifactDirs(runID)).NotTo(ContainElement(ContainSubstring("/" + flakyReport + "_")))

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(os.ReadFile(filepath.Join(artifactDir(env, runID, flakyReport), "artifact.zip"))).To(Equal(recordedZip(flakyReport)))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when the listing gives an artifact no digest", Label("artifacts"), func() {
	It("publishes its zip without verifying it", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(scenario.WithoutDigest(scenario.Recorded(runID, "after-attempt-1"), 11276401837))).To(Succeed())
		env.Fake.Fail("blob", flakyReportBlob, fakegithub.Fault{Status: http.StatusOK, Body: "not the recorded zip"})

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(os.ReadFile(filepath.Join(artifactDir(env, runID, flakyReport), "artifact.zip"))).To(BeEquivalentTo("not the recorded zip"))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when the listing gives an artifact a sha512 digest", Label("artifacts"), func() {
	It("publishes its zip unverified", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(scenario.WithDigest(scenario.Recorded(runID, "after-attempt-1"), 11276401837, "sha512:"+strings.Repeat("ab", 64)))).To(Succeed())
		env.Fake.Fail("blob", flakyReportBlob, fakegithub.Fault{Status: http.StatusOK, Body: "not the recorded zip"})

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(os.ReadFile(filepath.Join(artifactDir(env, runID, flakyReport), "artifact.zip"))).To(BeEquivalentTo("not the recorded zip"))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when the listing gives an artifact a malformed sha256 digest", Label("artifacts"), func() {
	It("publishes no artifact dir, requests no zip for it, and returns the failure, naming the artifact", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(scenario.WithDigest(scenario.Recorded(runID, "after-attempt-1"), 11276401837, "sha256:ZZ"))).To(Succeed())

		Expect(env.Sync(ctx)).To(MatchError(`run 37129390741 artifact 11276401837: unrecognized digest "sha256:ZZ"`))
		Expect(env.ArtifactDirs(runID)).To(HaveLen(3))
		Expect(env.ArtifactDirs(runID)).NotTo(ContainElement(ContainSubstring("/" + flakyReport + "_")))
		Expect(requestedZip(env, flakyReport)).To(BeFalse())
	}, cycleTimeout)
})

func recordedZip(artifactID string) []byte {
	GinkgoHelper()
	recorded, err := os.ReadFile(filepath.Join(recordings.Dir(runID, "after-attempt-1"), "artifacts", artifactID+".zip"))
	Expect(err).NotTo(HaveOccurred())
	return recorded
}

var _ = DescribeTable("mirror.Cycle when a zip fails transiently publishes no artifact dir and no tombstone for it, and the next cycle downloads it", Label("artifacts"),
	func(ctx SpecContext, host, match string, status int) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail(host, match, fakegithub.Fault{Status: status, Times: 1})

		Expect(env.Sync(ctx)).To(BeTransient())
		Expect(env.ArtifactDirs(runID)).To(HaveLen(3))
		Expect(env.Tombstones()).NotTo(ContainElement(ContainSubstring("/" + flakyReport + "_")))

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(os.ReadFile(filepath.Join(artifactDir(env, runID, flakyReport), "artifact.zip"))).To(Equal(recordedZip(flakyReport)))
	},
	Entry("API 500", "api", "artifacts/"+flakyReport+"/zip", http.StatusInternalServerError, cycleTimeout),
	Entry("blob 500", "blob", flakyReportBlob, http.StatusInternalServerError, cycleTimeout),
	Entry("blob 403", "blob", flakyReportBlob, http.StatusForbidden, cycleTimeout),
)

var _ = Describe("mirror.Cycle when a zip is rate limited", Label("artifacts"), func() {
	It("blocks the cycle and writes no tombstone for it", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", "artifacts/"+flakyReport+"/zip", fakegithub.Fault{Status: http.StatusTooManyRequests, Headers: map[string]string{"Retry-After": "30"}})

		Expect(env.Sync(ctx)).To(BeBlocked(failure.RateLimit))
		Expect(env.Tombstones()).NotTo(ContainElement(ContainSubstring("/" + flakyReport + "_")))
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when one log returns 500", Label("artifacts"), func() {
	It("still publishes the run's artifacts in that cycle", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("api", ranJobLog, fakegithub.Fault{Status: http.StatusInternalServerError})

		Expect(env.Sync(ctx)).To(BeTransient())
		Expect(env.AttemptDirs(runID)).To(BeEmpty())
		Expect(env.ArtifactDirs(runID)).To(HaveLen(4))
	}, cycleTimeout)
})

var _ = Describe("an artifact's fetch.json", Label("artifacts"), func() {
	It("records the PR numbers of the run as listed", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.AddRun(scenario.WithPullRequests(scenario.Recorded(runID, "after-attempt-1"), 42, 7))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		raw, err := os.ReadFile(filepath.Join(env.ArtifactDirs(runID)[0], "fetch.json"))
		Expect(err).NotTo(HaveOccurred())
		var fetch struct {
			PRNumbers []int `json:"pr_numbers"`
		}
		Expect(json.Unmarshal(raw, &fetch)).To(Succeed())
		Expect(fetch.PRNumbers).To(Equal([]int{42, 7}))
	}, cycleTimeout)
})
