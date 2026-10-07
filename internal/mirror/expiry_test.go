package mirror_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	. "github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

// expiredArtifacts are the expires-in-1-day artifacts of each run, delisted at after-expiry.
var expiredArtifacts = map[int64]string{runID: "11276327411", deletedRun: "11276237903"}

// lastStageBeforeExpiry is the stage of each run that still lists its expiredArtifacts.
var lastStageBeforeExpiry = map[int64]string{runID: "after-attempt-3", deletedRun: "logs-deleted"}

func advanceToExpiry(env *harness.InProcessEnv) {
	GinkgoHelper()
	for run := range expiredArtifacts {
		Expect(env.Fake.Advance(run, "after-expiry")).To(Succeed())
	}
	recordedAt, err := recordings.RecordedAt(deletedRun, "after-expiry")
	Expect(err).NotTo(HaveOccurred())
	env.Clock.Set(recordedAt)
}

var _ = Describe("mirror.Cycle at after-expiry", Label("artifacts"), func() {
	It("tombstones 11276327411 and 11276237903 as expired when a retry follows their expiry, and writes nothing for them when the run is first listed after it", func(ctx SpecContext) {
		By("retrying an artifact that was listed before it expired")
		env := harness.InProcess()
		for run, stage := range lastStageBeforeExpiry {
			Expect(env.Fake.Load(run, stage)).To(Succeed())
			env.Fake.Fail("api", "artifacts/"+expiredArtifacts[run]+"/zip", fakegithub.Fault{Status: http.StatusInternalServerError, Times: 1})
		}
		Expect(env.Sync(ctx)).To(BeTransient())
		advanceToExpiry(env)

		Expect(env.Sync(ctx)).To(Succeed())
		for run, id := range expiredArtifacts {
			Expect(readZipTombstone(env, run, id)).To(SatisfyAll(
				HaveKeyWithValue("reason", "expired"),
				HaveKeyWithValue("http_status", BeEquivalentTo(http.StatusGone)),
				HaveKeyWithValue("message", "Artifact has expired"),
				HaveKeyWithValue("url", zipURL(env, id)),
			), "artifact %s", id)
		}

		By("first listing the run after the artifact expired")
		fresh := harness.InProcess()
		for run := range expiredArtifacts {
			Expect(fresh.Fake.Load(run, "after-expiry")).To(Succeed())
		}
		advanceToExpiry(fresh)

		Expect(fresh.Sync(ctx)).To(Succeed())
		for run, id := range expiredArtifacts {
			Expect(fresh.ArtifactDirs(run)).NotTo(BeEmpty())
			Expect(fresh.ArtifactDirs(run)).NotTo(ContainElement(ContainSubstring("/" + id + "_")))
			Expect(fresh.Tombstones()).NotTo(ContainElement(ContainSubstring(id)))
			Expect(requestedZip(fresh, id)).To(BeFalse())
		}
	}, cycleTimeout)
})
