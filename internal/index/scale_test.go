package index_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/layout"
)

// writeScaleStore writes runs of one attempt, with two or three jobs and one
// artifact each: 100,000 files for 9,000 runs.
func writeScaleStore(data string, runs int) (files int) {
	GinkgoHelper()
	write := func(path, content string) {
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
		files++
	}
	for i := range runs {
		id := int64(1_000_000 + i)
		created := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * 15 * time.Minute)
		at := created.Format(time.RFC3339)
		runDir := filepath.Join(data, "github.com", "o", "r", "runs", created.Format(time.DateOnly), fmt.Sprintf("%d_ci_main", id))
		attempt := layout.AttemptDir(runDir, 1)
		write(filepath.Join(attempt, "attempt.json"), fmt.Sprintf(`{"id":%d,"name":"ci","head_branch":"main","head_sha":"%040d","event":"push","status":"completed","conclusion":"success","workflow_id":7,"pull_requests":[],"display_title":"commit %d","created_at":%q,"updated_at":%q,"run_started_at":%q,"run_attempt":1,"repository":{"full_name":"o/r"}}`, id, id, i, at, at, at))
		var jobsJSON []string
		jobs := 2
		if i%18 == 17 {
			jobs = 3
		}
		for j := range jobs {
			jobID := id*10 + int64(j)
			job := fmt.Sprintf(`{"id":%d,"run_id":%d,"run_attempt":1,"name":"build %d","status":"completed","conclusion":"success","started_at":%q,"completed_at":%q,"runner_name":"r","labels":["ubuntu-latest"],"steps":[{"name":"Run","status":"completed","conclusion":"success","number":1,"started_at":%q,"completed_at":%q}]}`, jobID, id, j, at, at, at, at)
			jobsJSON = append(jobsJSON, job)
			jobDir := layout.JobDir(attempt, jobID, fmt.Sprintf("build %d", j))
			write(filepath.Join(jobDir, "job.json"), job)
			write(filepath.Join(jobDir, "log.txt"), "\ufeff"+at+" build output\n")
		}
		artifact := fmt.Sprintf(`{"id":%d,"name":"report","size_in_bytes":22,"expired":false,"created_at":%q,"workflow_run":{"id":%d,"head_branch":"main","head_sha":"%040d"}}`, id, at, id, id)
		write(filepath.Join(attempt, "jobs.json"), "["+strings.Join(jobsJSON, ",")+"]")
		write(filepath.Join(attempt, "artifacts.json"), "["+artifact+"]")
		fetch := fmt.Sprintf(`{"lg_format":1,"host":"github.com","repo":"o/r","run_id":%d,"run_created_at":%q,"run_attempt_at_fetch":1,"run_status_at_fetch":"completed"`, id, at)
		write(filepath.Join(attempt, "fetch.json"), fetch+`,"attempt":1,"sources":{},"carried_forward_jobs":[]}`)
		artifactDir := layout.ArtifactDir(runDir, id, "report")
		write(filepath.Join(artifactDir, "artifact.json"), artifact)
		write(filepath.Join(artifactDir, "artifact.zip"), "PK\x05\x06"+string(make([]byte, 18)))
		write(filepath.Join(artifactDir, "fetch.json"), fetch+`,"workflow_id":7,"workflow_name":"ci","event":"push","pr_numbers":[],"display_title":"t","sources":{}}`)
	}
	return files
}

var _ = Describe("a store of 9,000 runs and 100,000 files", Label("scale"), func() {
	It("reconciles a no-op in under 1s and rebuilds in under 60s", func(ctx SpecContext) {
		data := filepath.Join(GinkgoT().TempDir(), "data")
		Expect(writeScaleStore(data, 9_000)).To(Equal(100_000))
		path := filepath.Join(GinkgoT().TempDir(), "lg.db")
		ix, err := index.Open(ctx, path, data, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)

		start := clock.Real{}.Now()
		Expect(index.Rebuild(ctx, path, data, nil)).To(Succeed())
		rebuilt := clock.Real{}.Now()
		Expect(ix.Reconcile(ctx)).To(Succeed())
		reconciled := clock.Real{}.Now()
		AddReportEntry("timings", fmt.Sprintf("rebuild %s, no-op reconcile %s", rebuilt.Sub(start), reconciled.Sub(rebuilt)))

		Expect(rebuilt.Sub(start)).To(BeNumerically("<", 60*time.Second))
		Expect(reconciled.Sub(rebuilt)).To(BeNumerically("<", time.Second))
		db := openDB(path)
		Expect(count(db, "SELECT count(*) FROM jobs")).To(Equal(18_500))
	}, NodeTimeout(5*time.Minute))
})

var _ = Describe("rerun flips over a store of 9,000 runs", Label("scale"), func() {
	It("gives them in under 2s", func(ctx SpecContext) {
		data := filepath.Join(GinkgoT().TempDir(), "data")
		writeScaleStore(data, 9_000)
		ix, err := index.Open(ctx, filepath.Join(GinkgoT().TempDir(), "lg.db"), data, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)
		Expect(ix.Reconcile(ctx)).To(Succeed())

		start := clock.Real{}.Now()
		flips, err := ix.RerunFlips(ctx, index.Filter{})
		took := clock.Real{}.Now().Sub(start)
		AddReportEntry("timings", fmt.Sprintf("rerun flips %s", took))

		Expect(err).NotTo(HaveOccurred())
		Expect(flips).To(BeEmpty())
		Expect(took).To(BeNumerically("<", 2*time.Second))
	}, NodeTimeout(5*time.Minute))
})
