package model_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

var _ = DescribeTable("RunRepositories.FromFork reports whether the head repository is another than the repository", Label("paths"),
	func(raw string, fork bool) {
		var run model.RunRepositories
		Expect(json.Unmarshal([]byte(raw), &run)).To(Succeed())
		Expect(run.FromFork()).To(Equal(fork))
	},
	Entry("the same repository", `{"repository":{"id":7},"head_repository":{"id":7}}`, false),
	Entry("a fork", `{"repository":{"id":7},"head_repository":{"id":8}}`, true),
	Entry("a deleted fork", `{"repository":{"id":7},"head_repository":null}`, true),
)

var _ = DescribeTable("ArtifactRun.FromFork reports whether the head repository is another than the repository", Label("paths"),
	func(raw string, fork bool) {
		var artifact model.Artifact
		Expect(json.Unmarshal([]byte(raw), &artifact)).To(Succeed())
		Expect(artifact.WorkflowRun.FromFork()).To(Equal(fork))
	},
	Entry("the same repository", `{"workflow_run":{"repository_id":7,"head_repository_id":7}}`, false),
	Entry("a fork", `{"workflow_run":{"repository_id":7,"head_repository_id":8}}`, true),
)

var _ = Describe("the recorded run and its artifacts", Label("paths"), func() {
	It("are not from a fork", func() {
		raw, err := os.ReadFile(filepath.Join(recordings.Dir(37129390741, "after-attempt-1"), "attempt-1", "attempt.json"))
		Expect(err).NotTo(HaveOccurred())
		var run model.RunRepositories
		Expect(json.Unmarshal(raw, &run)).To(Succeed())
		Expect(run.FromFork()).To(BeFalse())
		artifacts, err := recordings.Artifacts(37129390741, "after-attempt-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(artifacts).To(HaveEach(WithTransform(func(a model.Artifact) bool { return a.WorkflowRun.FromFork() }, BeFalse())))
	})
})
