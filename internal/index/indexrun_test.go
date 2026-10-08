package index_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/model"
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
		got, err := index.IndexRun(elsewhere)
		Expect(err).NotTo(HaveOccurred())
		// A copy's unit dirs have new mtimes.
		Expect(unitPaths(got)).To(Equal(unitPaths(want)))
		got.Units = want.Units
		Expect(got).To(Equal(want))
		Expect(want.Run).To(MatchFields(IgnoreExtras, Fields{
			"Host":    Equal("github.com"),
			"Repo":    Equal("rosenhouse/Lg"),
			"RunID":   BeEquivalentTo(runID),
			"DateDir": Equal("2026-10-03"),
		}))
		Expect(unitPaths(want)).To(ConsistOf(
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

	It("gives the run row pr_numbers the union of every attempt's pull_requests and commit_pr_numbers and every artifact's pr_numbers", Label("prs"), func() {
		dir := filepath.Join(GinkgoT().TempDir(), "5_ci_main")
		writeAttempt(dir, 1, "first", 1)
		Expect(os.WriteFile(filepath.Join(layout.AttemptDir(dir, 1), "fetch.json"), []byte(attemptFetch(1, `"commit_pr_numbers":[4,1],`)), 0o644)).To(Succeed())
		// Attempt 2's fetch.json, as an older lg wrote it, has no commit_pr_numbers.
		writeAttempt(dir, 2, "second", 2)
		writeArtifact(dir, 1, attemptStart(1).Format(time.RFC3339), `"pr_numbers":[5,2]`, nil)

		rows, err := index.IndexRun(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows.Run.PRNumbers).To(Equal([]int{1, 2, 4, 5}))
	})

	It("attributes an artifact created after attempt 1 started, fetched during attempt 2, to attempt 1 only once attempt 2 is on disk", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "5_ci_main")
		writeAttempt(dir, 1, "first")
		writeArtifact(dir, 1, attemptStart(1).Add(30*time.Minute).Format(time.RFC3339), `"run_attempt_at_fetch":2`, nil)
		attribution := func() []index.Artifact {
			rows, err := index.IndexRun(dir)
			Expect(err).NotTo(HaveOccurred())
			return rows.Artifacts
		}
		Expect(attribution()).To(ConsistOf(MatchFields(IgnoreExtras, Fields{"AttributedAttempt": BeZero(), "Attribution": Equal(model.Unknown)})))

		writeAttempt(dir, 2, "second")
		Expect(attribution()).To(ConsistOf(MatchFields(IgnoreExtras, Fields{"AttributedAttempt": Equal(1), "Attribution": Equal(model.ByTimestamp)})))
	})

	It("gives a run with only artifacts the facts of the artifact fetched last", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "5_ci_main")
		writeArtifact(dir, 1, "2026-10-01T00:00:00Z", `"run_attempt_at_fetch":1,"fetched_at":"2026-10-01T01:00:00Z","workflow_name":"old","display_title":"old"`, nil)
		writeArtifact(dir, 2, "2026-10-01T00:00:00Z", `"run_attempt_at_fetch":1,"fetched_at":"2026-10-01T02:00:00Z","workflow_name":"new","display_title":"new"`, nil)

		rows, err := index.IndexRun(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows.Run).To(MatchFields(IgnoreExtras, Fields{"WorkflowName": Equal("new"), "DisplayTitle": Equal("new")}))
	})

	It("gives an artifact row has_zip, expired from its zip's tombstone, and extracted when extracted/ exists", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "5_ci_main")
		writeArtifact(dir, 1, "2026-10-01T00:00:00Z", `"run_attempt_at_fetch":1`,
			map[string]string{"artifact.zip.tombstone": `{"lg_format":1,"reason":"expired","http_status":410,"tombstoned_at":"2026-10-02T00:00:00Z"}`})
		writeArtifact(dir, 2, "2026-10-01T00:00:00Z", `"run_attempt_at_fetch":1`, map[string]string{"artifact.zip": "PK"})
		Expect(os.Mkdir(filepath.Join(dir, "artifacts/2_a/extracted"), 0o755)).To(Succeed())

		rows, err := index.IndexRun(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows.Artifacts).To(ConsistOf(
			MatchFields(IgnoreExtras, Fields{"ArtifactID": BeEquivalentTo(1), "HasZip": BeFalse(), "Expired": BeTrue(), "Extracted": BeFalse()}),
			MatchFields(IgnoreExtras, Fields{"ArtifactID": BeEquivalentTo(2), "HasZip": BeTrue(), "Expired": BeFalse(), "Extracted": BeTrue()}),
		))
		Expect(unitPaths(rows)).To(ConsistOf("artifacts/1_a", "artifacts/2_a", "artifacts/2_a/extracted"))
		Expect(rows.Tombstones).To(ConsistOf(MatchAllFields(Fields{
			"Path":         Equal("artifacts/1_a/artifact.zip.tombstone"),
			"Reason":       Equal("expired"),
			"HTTPStatus":   PointTo(Equal(410)),
			"TombstonedAt": Equal(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)),
		})))
	})

	It("ignores a dir beside the attempts whose name has no attempt number", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "5_ci_main")
		writeAttempt(dir, 1, "first")
		Expect(os.Mkdir(filepath.Join(dir, "attempt-x"), 0o755)).To(Succeed())

		rows, err := index.IndexRun(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(unitPaths(rows)).To(Equal([]string{"attempt-1"}))
	})
})

func unitPaths(rows index.Rows) []string {
	var paths []string
	for _, u := range rows.Units {
		paths = append(paths, u.Path)
	}
	return paths
}

// writeArtifact writes a hand-made artifact of run 5 named a, created at
// createdAt, whose fetch.json adds the members fetch, and the files extra.
func writeArtifact(runDir string, id int64, createdAt, fetch string, extra map[string]string) {
	GinkgoHelper()
	dir := layout.ArtifactDir(runDir, id, "a")
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	files := map[string]string{
		"artifact.json": fmt.Sprintf(`{"id":%d,"name":"a","created_at":%q}`, id, createdAt),
		"fetch.json":    `{"lg_format":1,"run_id":5,"run_created_at":"2026-10-01T00:00:00Z",` + fetch + `}`,
	}
	maps.Copy(files, extra)
	for name, content := range files {
		Expect(os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)).To(Succeed())
	}
}

// writeAttempt writes a hand-made attempt n of run 5 with no jobs and no
// artifacts. It starts n hours into 2026-10-02, after the run's
// created_at.
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
			"created_at":%[4]q,"run_started_at":%[4]q,"updated_at":%[4]q,
			"run_attempt":%[2]d,"repository":{"full_name":"o/r"}}`, label, n, prsJSON, attemptStart(n).Format(time.RFC3339)),
		"jobs.json":      `[]`,
		"artifacts.json": `[]`,
		"fetch.json":     attemptFetch(n, ""),
	}
	for name, content := range files {
		Expect(os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)).To(Succeed())
	}
}

// attemptFetch is attempt n's fetch.json, with extra fields first.
func attemptFetch(n int, extra string) string {
	return fmt.Sprintf(`{%s"lg_format":1,"host":"example.com","repo":"o/r","run_id":5,"run_created_at":"2026-09-30T23:59:59Z",
		"run_attempt_at_fetch":%d,"run_status_at_fetch":"completed","attempt":%d,"sources":{},"carried_forward_jobs":[]}`, extra, n, n)
}

func attemptStart(n int) time.Time { return time.Date(2026, 10, 2, n, 0, 0, 0, time.UTC) }
