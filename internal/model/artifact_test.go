package model_test

import (
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

var _ = DescribeTable("Artifact.SHA256 parses a sha256:<hex> digest, and gives none to verify against otherwise", Label("artifacts"),
	func(digest, sum string, ok bool) {
		gotSum, gotOK := model.Artifact{Digest: digest}.SHA256()
		Expect(gotOK).To(Equal(ok))
		Expect(gotSum).To(Equal(sum))
	},
	Entry("recorded", "sha256:d9df8b557651cd5b643604d89ba20ea3cb8d7e3636d28a1377c2a38254865f6e", "d9df8b557651cd5b643604d89ba20ea3cb8d7e3636d28a1377c2a38254865f6e", true),
	Entry("upper case", "sha256:D9DF8B557651CD5B643604D89BA20EA3CB8D7E3636D28A1377C2A38254865F6E", "d9df8b557651cd5b643604d89ba20ea3cb8d7e3636d28a1377c2a38254865f6e", true),
	Entry("missing", "", "", false),
	Entry("another algorithm", "sha512:d9df8b557651cd5b643604d89ba20ea3cb8d7e3636d28a1377c2a38254865f6e", "", false),
	Entry("too short", "sha256:d9df", "", false),
	Entry("not hex", "sha256:z9df8b557651cd5b643604d89ba20ea3cb8d7e3636d28a1377c2a38254865f6e", "", false),
)
