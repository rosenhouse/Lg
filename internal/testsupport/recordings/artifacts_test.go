package recordings_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = Describe("Artifacts", Label("artifacts"), func() {
	It("reads a stage's artifacts.json", func() {
		artifacts, err := recordings.Artifacts(37129390741, "after-attempt-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(artifacts).To(HaveLen(4))
		Expect(artifacts[3]).To(Equal(model.Artifact{
			ID:          11275917910,
			Name:        "expires-in-1-day",
			SizeInBytes: 169,
			CreatedAt:   time.Date(2026, 10, 3, 14, 23, 2, 0, time.UTC),
			ExpiresAt:   time.Date(2026, 10, 4, 14, 23, 1, 0, time.UTC),
			Digest:      "sha256:fd151fe190b853dc69d973a927588591cdab0e90cd5320abe040b70fc82ce765",
			WorkflowRun: model.ArtifactRun{
				RepositoryID: 1402714635, HeadRepositoryID: 1402714635, HeadBranch: "lg-fixture", HeadSHA: "1a51097dadb5b55978ac401b93f1ca9d8d317b02",
			},
		}))
	})

	It("fails for a stage with no artifacts.json", func() {
		_, err := recordings.Artifacts(37129390741, "no-such-stage")
		Expect(err).To(MatchError(ContainSubstring("artifacts.json")))
	})
})
