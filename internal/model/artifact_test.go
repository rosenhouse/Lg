package model_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
)

var _ = DescribeTable("ClassifyArtifact checks expired, then too_large, then download", Label("artifacts"),
	func(artifact model.Artifact, action model.ArtifactAction) {
		Expect(model.ClassifyArtifact(artifact, 700)).To(Equal(action))
	},
	Entry("expired and too large", model.Artifact{Expired: true, SizeInBytes: 701}, model.ArtifactExpired),
	Entry("expired", model.Artifact{Expired: true, SizeInBytes: 169}, model.ArtifactExpired),
	Entry("one byte over the cap", model.Artifact{SizeInBytes: 701}, model.ArtifactTooLarge),
	Entry("at the cap", model.Artifact{SizeInBytes: 700}, model.ArtifactDownload),
	Entry("empty", model.Artifact{}, model.ArtifactDownload),
)

const recordedSum = "d9df8b557651cd5b643604d89ba20ea3cb8d7e3636d28a1377c2a38254865f6e"

var _ = DescribeTable("Artifact.SHA256 parses a sha256:<hex> digest", Label("artifacts"),
	func(digest string) {
		Expect(model.Artifact{Digest: digest}.SHA256()).To(Equal(recordedSum))
	},
	Entry("recorded", "sha256:"+recordedSum),
	Entry("upper case hex", "sha256:D9DF8B557651CD5B643604D89BA20EA3CB8D7E3636D28A1377C2A38254865F6E"),
	Entry("upper case algorithm", "SHA256:"+recordedSum),
)

var _ = It("Artifact.SHA256 gives no sum to verify against for a missing digest", Label("artifacts"), func() {
	Expect(model.Artifact{}.SHA256()).To(BeEmpty())
})

var _ = DescribeTable("Artifact.SHA256 refuses any other digest", Label("artifacts"),
	func(digest string) {
		_, err := model.Artifact{Digest: digest}.SHA256()
		Expect(err).To(MatchError(fmt.Sprintf("unrecognized digest %q", digest)))
	},
	Entry("another algorithm", "sha512:"+recordedSum),
	Entry("too short", "sha256:d9df"),
	Entry("not hex", "sha256:z9df8b557651cd5b643604d89ba20ea3cb8d7e3636d28a1377c2a38254865f6e"),
	Entry("no algorithm", recordedSum),
)
