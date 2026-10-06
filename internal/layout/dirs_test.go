package layout_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
)

var _ = Describe("RepoDir", Label("sync"), func() {
	It("is data/<host lowercased>/<full_name as GitHub spells it>", func() {
		Expect(layout.RepoDir("/d", "GitHub.com", "rosenhouse/Lg")).To(Equal("/d/github.com/rosenhouse/Lg"))
	})
})

var _ = Describe("RunDir", Label("sync"), func() {
	run := model.Run{
		ID:         37129390741,
		Name:       "CI",
		Path:       ".github/workflows/lg-fixture.yml",
		HeadBranch: "feat/x",
		CreatedAt:  time.Date(2026, 10, 3, 23, 30, 0, 0, time.FixedZone("-05:00", -5*60*60)),
	}

	It("is runs/<UTC date of created_at>/<id>_<workflow slug>_<branch slug>", func() {
		Expect(layout.RunDir("/r", run)).To(Equal("/r/runs/2026-10-04/37129390741_CI_feat-x"))
	})

	It("slugs the workflow path's basename without its extension when the run has no name", func() {
		unnamed := run
		unnamed.Name = ""
		Expect(layout.RunDir("/r", unnamed)).To(Equal("/r/runs/2026-10-04/37129390741_lg-fixture_feat-x"))
	})
})

var _ = Describe("AttemptDir and JobDir", Label("sync"), func() {
	It("are attempt-<n> and jobs/<id>_<name slug>", func() {
		Expect(layout.AttemptDir("/run", 2)).To(Equal("/run/attempt-2"))
		Expect(layout.JobDir("/run/attempt-2", 111, "build (x)")).To(Equal("/run/attempt-2/jobs/111_build-x"))
	})
})

var _ = Describe("ArtifactDir", Label("artifacts"), func() {
	It("is artifacts/<id>_<name slug>", func() {
		Expect(layout.ArtifactDir("/run", 11276401837, "flaky report (x)")).To(Equal("/run/artifacts/11276401837_flaky-report-x"))
	})
})

var _ = DescribeTable("AttemptNumber gives n for a dir named as AttemptDir names attempt n", Label("attempts"),
	func(name string, n int, ok bool) {
		got, gotOK := layout.AttemptNumber(name)
		Expect(gotOK).To(Equal(ok))
		Expect(got).To(Equal(n))
	},
	Entry("attempt-2", "attempt-2", 2, true),
	Entry("attempt-12", "attempt-12", 12, true),
	Entry("a leading zero", "attempt-02", 0, false),
	Entry("a sign", "attempt-+2", 0, false),
	Entry("no number", "attempt-", 0, false),
	Entry("another name", "fetch.json", 0, false),
)

var _ = DescribeTable("DirID gives the id of a dir named as RunDir, JobDir or ArtifactDir names it", Label("status"),
	func(name string, id int64, ok bool) {
		got, gotOK := layout.DirID(name)
		Expect(gotOK).To(Equal(ok))
		Expect(got).To(Equal(id))
	},
	Entry("an artifact", "11276401837_flaky-report-x", int64(11276401837), true),
	Entry("a run", "37129390741_ci_main", int64(37129390741), true),
	Entry("an empty slug", "7_", int64(7), true),
	Entry("a leading zero", "07_x", int64(0), false),
	Entry("a sign", "+7_x", int64(0), false),
	Entry("no underscore", "7", int64(0), false),
	Entry("no number", "_x", int64(0), false),
	Entry("another name", "fetch.json", int64(0), false),
)
