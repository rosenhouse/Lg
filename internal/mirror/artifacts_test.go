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

// expiresIn1DayAt is expires_at of artifact 11275917910.
var expiresIn1DayAt = time.Date(2026, 10, 4, 14, 23, 1, 0, time.UTC)

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
	raw, err := os.ReadFile(filepath.Join(dir, "artifact.zip.tombstone"))
	Expect(err).NotTo(HaveOccurred())
	var tombstone map[string]any
	Expect(json.Unmarshal(raw, &tombstone)).To(Succeed())
	return tombstone
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
		Expect(glob(filepath.Join(env.Data(), "*", "*", "*", "runs", "*", "1_*", "artifacts", "*", "artifact.zip"))).To(HaveLen(4))
		Expect(env.AttemptDirs(cloneID)).To(BeEmpty())
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
		Expect(readZipTombstone(env, runID, flakyReport)).To(HaveKeyWithValue("reason", "too_large"))
		Expect(os.ReadDir(env.Tmp())).To(BeEmpty())
	}, cycleTimeout)
})

var _ = Describe("mirror.Cycle when the listing says expired: true", Label("artifacts"), func() {
	It("writes an expired tombstone without a request", func(ctx SpecContext) {
		env := harness.InProcess()
		run := scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), cloneID)
		Expect(env.Fake.AddRun(scenario.Expire(run, 1_011275917910))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(readZipTombstone(env, cloneID, "1011275917910")).To(SatisfyAll(
			HaveKeyWithValue("reason", "expired"),
			HaveKeyWithValue("http_status", BeNil()),
		))
		Expect(requestedZip(env, "1011275917910")).To(BeFalse())
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
			HaveKeyWithValue("url", env.Fake.URL()+"/repos/rosenhouse/lg/actions/artifacts/"+expiresIn1Day+"/zip"),
		))
	},
	Entry("410: expired", http.StatusGone, harness.DefaultNow(), "expired", cycleTimeout),
	Entry("404 with expires_at in the past: expired", http.StatusNotFound, expiresIn1DayAt.Add(time.Hour), "expired", cycleTimeout),
	Entry("404 with expires_at in the future: deleted", http.StatusNotFound, harness.DefaultNow(), "deleted", cycleTimeout),
)

var _ = Describe("mirror.Cycle when a zip does not match its digest", Label("artifacts"), func() {
	It("publishes no artifact dir, and the next cycle downloads it", func(ctx SpecContext) {
		env := harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		env.Fake.Fail("blob", flakyReportBlob, fakegithub.Fault{Status: http.StatusOK, Body: "not the recorded zip", Times: 1})

		Expect(env.Sync(ctx)).To(BeTransient())
		Expect(env.ArtifactDirs(runID)).To(HaveLen(3))
		Expect(env.ArtifactDirs(runID)).NotTo(ContainElement(ContainSubstring("/" + flakyReport + "_")))

		Expect(env.Sync(ctx)).To(Succeed())
		recorded, err := os.ReadFile(filepath.Join(recordings.Dir(runID, "after-attempt-1"), "artifacts", flakyReport+".zip"))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.ReadFile(filepath.Join(artifactDir(env, runID, flakyReport), "artifact.zip"))).To(Equal(recorded))
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
