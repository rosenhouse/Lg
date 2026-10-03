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
		Name:       "lg fixture",
		Path:       ".github/workflows/lg-fixture.yml",
		HeadBranch: "feat/x",
		CreatedAt:  time.Date(2026, 10, 3, 23, 30, 0, 0, time.FixedZone("-05:00", -5*60*60)),
	}

	It("is runs/<UTC date of created_at>/<id>_<workflow slug>_<branch slug>", func() {
		Expect(layout.RunDir("/r", run)).To(Equal("/r/runs/2026-10-04/37129390741_lg-fixture_feat-x"))
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
