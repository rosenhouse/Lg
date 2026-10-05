package index_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("index.Reconcile when a run's file does not parse", Label("index"), func() {
	It("indexes the other runs, names the file, and indexes the run once the file parses", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		attemptJSON := filepath.Join(layout.AttemptDir(runDir(env.Data(), runID), 1), "attempt.json")
		content, err := os.ReadFile(attemptJSON)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(attemptJSON, []byte("<"), 0o644)).To(Succeed())
		ix, err := index.Open(dbPath(env), env.Data())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)

		Expect(ix.Reconcile(ctx)).To(MatchError(ContainSubstring(attemptJSON)))
		db := openDB(dbPath(env))
		Expect(column[int64](db, "SELECT run_id FROM runs")).To(Equal([]int64{deletedRun}))

		Expect(os.WriteFile(attemptJSON, content, 0o644)).To(Succeed())
		Expect(ix.Reconcile(ctx)).To(Succeed())
		Expect(column[int64](db, "SELECT run_id FROM runs")).To(ConsistOf(int64(runID), int64(deletedRun)))
	}, syncTimeout)
})
