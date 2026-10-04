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
