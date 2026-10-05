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

var _ = Describe("index.Reconcile failing", Label("index"), func() {
	It("on a file that does not parse indexes the other runs, names the file, and indexes the run once the file parses", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
		attemptJSON := filepath.Join(layout.AttemptDir(runDir(env.Data(), runID), 1), "attempt.json")
		content, err := os.ReadFile(attemptJSON)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(attemptJSON, []byte("<"), 0o644)).To(Succeed())
		ix, err := index.Open(ctx, dbPath(env), env.Data())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)

		Expect(ix.Reconcile(ctx)).To(MatchError(ContainSubstring(attemptJSON)))
		db := openDB(dbPath(env))
		Expect(column[int64](db, "SELECT run_id FROM runs")).To(Equal([]int64{deletedRun}))

		Expect(os.WriteFile(attemptJSON, content, 0o644)).To(Succeed())
		Expect(ix.Reconcile(ctx)).To(Succeed())
		Expect(column[int64](db, "SELECT run_id FROM runs")).To(ConsistOf(int64(runID), int64(deletedRun)))
	}, syncTimeout)

	It("names lg.db when the db fails", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
		reconcile(ctx, dbPath(env), env.Data())
		_, err := openDB(dbPath(env)).ExecContext(ctx, "DROP TABLE units")
		Expect(err).NotTo(HaveOccurred())
		ix, err := index.Open(ctx, dbPath(env), env.Data())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)

		Expect(ix.Reconcile(ctx)).To(MatchError(ContainSubstring(dbPath(env) + ": ")))
	}, syncTimeout)
})

var _ = Describe("index.Reconcile", Label("index"), func() {
	It("gives the rows a rebuild gives after a run is re-published under the same unit paths", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
		reconcile(ctx, dbPath(env), env.Data())
		attempt := layout.AttemptDir(runDir(env.Data(), runID), 1)
		staged := filepath.Join(env.Tmp(), "staged")
		Expect(os.CopyFS(staged, os.DirFS(attempt))).To(Succeed())
		job := layout.JobDir(staged, 111221289888, "flaky")
		Expect(os.Remove(filepath.Join(job, "log.txt"))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(job, "log.txt.tombstone"),
			[]byte(`{"lg_format":1,"reason":"expired","http_status":410,"tombstoned_at":"2026-10-03T18:00:00Z"}`), 0o644)).To(Succeed())
		Expect(os.RemoveAll(attempt)).To(Succeed())
		Expect(os.Rename(staged, attempt)).To(Succeed())

		reconcile(ctx, dbPath(env), env.Data())
		rebuilt := filepath.Join(GinkgoT().TempDir(), "lg.db")
		Expect(index.Rebuild(ctx, rebuilt, env.Data())).To(Succeed())
		Expect(contents(openDB(dbPath(env)), env.Data())).To(Equal(contents(openDB(rebuilt), env.Data())))
	}, syncTimeout)
})
