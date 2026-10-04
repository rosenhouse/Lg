package model_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = DescribeTable("ClassifyArtifact checks expired: true, which the docs describe and no recording shows, then too_large, then download", Label("artifacts"),
	func(artifact model.Artifact, action model.ArtifactAction) {
		Expect(model.ClassifyArtifact(artifact, 700)).To(Equal(action))
	},
	Entry("expired and too large", model.Artifact{Expired: true, SizeInBytes: 701}, model.ArtifactExpired),
	Entry("expired", model.Artifact{Expired: true, SizeInBytes: 169}, model.ArtifactExpired),
	Entry("one byte over the cap", model.Artifact{SizeInBytes: 701}, model.ArtifactTooLarge),
	Entry("at the cap", model.Artifact{SizeInBytes: 700}, model.ArtifactDownload),
	Entry("empty", model.Artifact{}, model.ArtifactDownload),
)

// recordedListing is a stage's artifacts.json.
func recordedListing(runID int64, stage string) []model.Artifact {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join(recordings.Dir(runID, stage), "artifacts.json"))
	Expect(err).NotTo(HaveOccurred())
	var listing struct{ Artifacts []model.Artifact }
	Expect(json.Unmarshal(raw, &listing)).To(Succeed())
	Expect(listing.Artifacts).NotTo(BeEmpty())
	return listing.Artifacts
}

var _ = Describe("ClassifyArtifact over the after-expiry recordings", Label("artifacts"), func() {
	const maxBytes = 500_000_000

	It("gives download for every listed artifact, since GitHub delists an expired artifact rather than listing it expired: true", func() {
		for _, run := range []int64{37129390741, 37129738159} {
			for _, artifact := range recordedListing(run, "after-expiry") {
				Expect(model.ClassifyArtifact(artifact, maxBytes)).To(Equal(model.ArtifactDownload), "artifact %d", artifact.ID)
			}
		}
	})

	It("gives download for 11276327411 as listed at after-attempt-3, although its expires_at passed before after-expiry: only its zip's 410 tells lg it expired", func() {
		listing := recordedListing(37129390741, "after-attempt-3")
		i := slices.IndexFunc(listing, func(a model.Artifact) bool { return a.ID == 11276327411 })
		Expect(i).To(BeNumerically(">=", 0))
		recordedAt, err := recordings.RecordedAt(37129390741, "after-expiry")
		Expect(err).NotTo(HaveOccurred())
		Expect(listing[i].ExpiresAt).To(BeTemporally("<", recordedAt))

		Expect(model.ClassifyArtifact(listing[i], maxBytes)).To(Equal(model.ArtifactDownload))
	})
})

const recordedSum = "d9df8b557651cd5b643604d89ba20ea3cb8d7e3636d28a1377c2a38254865f6e"

var _ = DescribeTable("Artifact.SHA256 parses a sha256:<hex> digest", Label("artifacts"),
	func(digest string) {
		Expect(model.Artifact{Digest: digest}.SHA256()).To(Equal(recordedSum))
	},
	Entry("recorded", "sha256:"+recordedSum),
	Entry("upper case hex", "sha256:D9DF8B557651CD5B643604D89BA20EA3CB8D7E3636D28A1377C2A38254865F6E"),
	Entry("upper case algorithm", "SHA256:"+recordedSum),
)

var _ = DescribeTable("Artifact.SHA256 gives no sum to verify against", Label("artifacts"),
	func(digest string) {
		Expect(model.Artifact{Digest: digest}.SHA256()).To(BeEmpty())
	},
	Entry("for a missing digest", ""),
	Entry("for a digest of another algorithm", "sha512:"+recordedSum+recordedSum),
)

var _ = DescribeTable("Artifact.SHA256 refuses any other digest", Label("artifacts"),
	func(digest string) {
		_, err := model.Artifact{Digest: digest}.SHA256()
		Expect(err).To(MatchError(fmt.Sprintf("unrecognized digest %q", digest)))
	},
	Entry("another algorithm, not hex", "sha512:zz"),
	Entry("too short", "sha256:d9df"),
	Entry("not hex", "sha256:z9df8b557651cd5b643604d89ba20ea3cb8d7e3636d28a1377c2a38254865f6e"),
	Entry("no algorithm", recordedSum),
)
