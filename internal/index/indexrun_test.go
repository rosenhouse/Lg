package index_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("IndexRun", Label("index"), func() {
	It("derives all rows of one run from that run's files only", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-2")
		dir := runDir(env.Data(), runID)
		elsewhere := filepath.Join(GinkgoT().TempDir(), filepath.Base(dir))
		Expect(os.CopyFS(elsewhere, os.DirFS(dir))).To(Succeed())

		want, err := index.IndexRun(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(index.IndexRun(elsewhere)).To(Equal(want))
		Expect(want.Run).To(MatchFields(IgnoreExtras, Fields{
			"Host":    Equal("github.com"),
			"Repo":    Equal("rosenhouse/Lg"),
			"RunID":   BeEquivalentTo(runID),
			"DateDir": Equal("2026-10-03"),
		}))
		Expect(want.Units).To(ConsistOf(
			"attempt-1", "attempt-2",
			"artifacts/11276401837_flaky-report", "artifacts/11276272069_pass-artifact",
			"artifacts/11275917910_expires-in-1-day",
			"artifacts/11276267449_flaky-report", "artifacts/11276052917_rerun-only-attempt-2",
			"artifacts/11275918123_flaky-report-overwrite",
		))
	}, syncTimeout)

	It("gives the run row workflow, branch, sha, event and display_title from the highest attempt on disk, pr_numbers the union, created_at from fetch.json and latest_attempt the max", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "5_ci_main")
		writeAttempt(dir, 2, "old", 1, 3)
		writeAttempt(dir, 10, "new", 3, 2)

		rows, err := index.IndexRun(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows.Run).To(MatchFields(IgnoreExtras, Fields{
			"WorkflowID":    BeEquivalentTo(10),
			"WorkflowName":  Equal("new"),
			"HeadBranch":    Equal("branch-new"),
			"HeadSHA":       Equal("sha-new"),
			"Event":         Equal("event-new"),
			"DisplayTitle":  Equal("title-new"),
			"PRNumbers":     Equal([]int{1, 2, 3}),
			"CreatedAt":     Equal(time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)),
			"DateDir":       Equal("2026-09-30"),
			"LatestAttempt": Equal(10),
		}))
	})
})

// writeAttempt writes a hand-made attempt of run 5 with no jobs and no
// artifacts, whose own created_at is a day after the run's.
func writeAttempt(runDir string, n int, label string, prs ...int) {
	GinkgoHelper()
	dir := layout.AttemptDir(runDir, n)
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	pullRequests := []map[string]int{}
	for _, pr := range prs {
		pullRequests = append(pullRequests, map[string]int{"number": pr})
	}
	prsJSON, err := json.Marshal(pullRequests)
	Expect(err).NotTo(HaveOccurred())
	files := map[string]string{
		"attempt.json": fmt.Sprintf(`{"id":5,"name":%[1]q,"head_branch":"branch-%[1]s","head_sha":"sha-%[1]s","event":"event-%[1]s",
			"display_title":"title-%[1]s","workflow_id":%[2]d,"pull_requests":%[3]s,"status":"completed","conclusion":"success",
			"created_at":"2026-10-01T23:59:59Z","run_started_at":"2026-10-01T23:59:59Z","updated_at":"2026-10-02T00:00:00Z",
			"run_attempt":%[2]d,"repository":{"full_name":"o/r"}}`, label, n, prsJSON),
		"jobs.json":      `[]`,
		"artifacts.json": `[]`,
		"fetch.json": fmt.Sprintf(`{"lg_format":1,"host":"example.com","repo":"o/r","run_id":5,"run_created_at":"2026-09-30T23:59:59Z",
			"run_attempt_at_fetch":%d,"run_status_at_fetch":"completed","attempt":%d,"sources":{},"carried_forward_jobs":[]}`, n, n),
	}
	for name, content := range files {
		Expect(os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)).To(Succeed())
	}
}
