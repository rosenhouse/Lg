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

// storeShape gives how many attempts and jobs each run has, and how many steps each job has.
type storeShape struct {
	attempts, jobs func(run int) int
	steps          int
}

// thinStore has runs of one attempt, with two or three jobs of one step:
// 100,000 files for 9,000 runs.
var thinStore = storeShape{
	attempts: func(int) int { return 1 },
	jobs: func(run int) int {
		if run%18 == 17 {
			return 3
		}
		return 2
	},
	steps: 1,
}

// writeScaleStore writes runs of the shape given, each with one artifact. In
// a run of more than one attempt, the first job and its first step fail in
// attempt 1 and every job is re-run.
func writeScaleStore(data string, runs int, shape storeShape) (files int) {
	GinkgoHelper()
	write := func(path, content string) {
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
		files++
	}
	for i := range runs {
		id := int64(1_000_000 + i)
		created := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * 15 * time.Minute)
		createdAt := created.Format(time.RFC3339)
		runDir := filepath.Join(data, "github.com", "o", "r", "runs", created.Format(time.DateOnly), fmt.Sprintf("%d_ci_main", id))
		attempts := shape.attempts(i)
		artifact := fmt.Sprintf(`{"id":%d,"name":"report","size_in_bytes":22,"expired":false,"created_at":%q,"workflow_run":{"id":%d,"head_branch":"main","head_sha":"%040d"}}`, id, createdAt, id, id)
		fetch := func(n int) string {
			return fmt.Sprintf(`{"lg_format":1,"host":"github.com","repo":"o/r","run_id":%d,"run_created_at":%q,"run_attempt_at_fetch":%d,"run_status_at_fetch":"completed"`, id, createdAt, n)
		}
		for n := 1; n <= attempts; n++ {
			at := created.Add(time.Duration(n-1) * time.Minute).Format(time.RFC3339)
			conclusion := func(job, step int) string {
				if n == 1 && attempts > 1 && job == 0 && step == 0 {
					return "failure"
				}
				return "success"
			}
			attempt := layout.AttemptDir(runDir, n)
			write(filepath.Join(attempt, "attempt.json"), fmt.Sprintf(`{"id":%d,"name":"ci","head_branch":"main","head_sha":"%040d","event":"push","status":"completed","conclusion":%q,"workflow_id":7,"pull_requests":[],"display_title":"commit %d","created_at":%q,"updated_at":%q,"run_started_at":%q,"run_attempt":%d,"repository":{"full_name":"o/r"}}`, id, id, conclusion(0, 0), i, createdAt, at, at, n))
			var jobsJSON []string
			for j := range shape.jobs(i) {
				jobID := (id*10+int64(n))*100 + int64(j)
				var steps []string
				for k := range shape.steps {
					steps = append(steps, fmt.Sprintf(`{"name":"step %d","status":"completed","conclusion":%q,"number":%d,"started_at":%q,"completed_at":%q}`, k, conclusion(j, k), k+1, at, at))
				}
				job := fmt.Sprintf(`{"id":%d,"run_id":%d,"run_attempt":%d,"name":"build %d","status":"completed","conclusion":%q,"started_at":%q,"completed_at":%q,"runner_name":"r","labels":["ubuntu-latest"],"steps":[%s]}`, jobID, id, n, j, conclusion(j, 0), at, at, strings.Join(steps, ","))
				jobsJSON = append(jobsJSON, job)
				jobDir := layout.JobDir(attempt, jobID, fmt.Sprintf("build %d", j))
				write(filepath.Join(jobDir, "job.json"), job)
				write(filepath.Join(jobDir, "log.txt"), "\ufeff"+at+" build output\n")
			}
			write(filepath.Join(attempt, "jobs.json"), "["+strings.Join(jobsJSON, ",")+"]")
			listed := ""
			if n == 1 {
				listed = artifact
			}
			write(filepath.Join(attempt, "artifacts.json"), "["+listed+"]")
			write(filepath.Join(attempt, "fetch.json"), fetch(n)+fmt.Sprintf(`,"attempt":%d,"sources":{},"carried_forward_jobs":[]}`, n))
		}
		artifactDir := layout.ArtifactDir(runDir, id, "report")
		write(filepath.Join(artifactDir, "artifact.json"), artifact)
		write(filepath.Join(artifactDir, "artifact.zip"), "PK\x05\x06"+string(make([]byte, 18)))
		write(filepath.Join(artifactDir, "fetch.json"), fetch(1)+`,"workflow_id":7,"workflow_name":"ci","event":"push","pr_numbers":[],"display_title":"t","sources":{}}`)
	}
	return files
}

var _ = Describe("a store of 9,000 runs and 100,000 files", Label("scale"), func() {
	It("reconciles a no-op in under 1s and rebuilds in under 60s", func(ctx SpecContext) {
		data := filepath.Join(GinkgoT().TempDir(), "data")
		Expect(writeScaleStore(data, 9_000, thinStore)).To(Equal(100_000))
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

var _ = Describe("rerun flips over a store of 9,000 runs of ten jobs of ten steps, 4% of them re-run", Label("scale"), func() {
	It("gives them in under 2s", func(ctx SpecContext) {
		data := filepath.Join(GinkgoT().TempDir(), "data")
		rerun := storeShape{
			attempts: func(run int) int {
				switch run % 50 {
				case 49:
					return 3
				case 24:
					return 2
				}
				return 1
			},
			jobs:  func(int) int { return 10 },
			steps: 10,
		}
		writeScaleStore(data, 9_000, rerun)
		ix, err := index.Open(ctx, filepath.Join(GinkgoT().TempDir(), "lg.db"), data, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)
		Expect(ix.Reconcile(ctx)).To(Succeed())

		start := clock.Real{}.Now()
		flips, err := ix.RerunFlips(ctx, index.Filter{})
		took := clock.Real{}.Now().Sub(start)
		AddReportEntry("timings", fmt.Sprintf("rerun flips %s", took))

		Expect(err).NotTo(HaveOccurred())
		Expect(flips).To(HaveLen(9_000 * 2 / 50 * 2))
		Expect(took).To(BeNumerically("<", 2*time.Second))
	}, NodeTimeout(5*time.Minute))
})
