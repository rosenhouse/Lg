package index_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
	"github.com/rosenhouse/lg/internal/testsupport/scenario"
)

var _ = Describe("IndexRun", Label("paths"), func() {
	It("marks a run from a fork, by its highest attempt or else by its artifacts", func(ctx SpecContext) {
		env := harness.InProcess()
		env.Mirror.Backfill = 60 * scenario.Day
		a := scenario.Archaeology()
		const pendingFork = 9
		running := scenario.FromFork(scenario.InProgress(scenario.CloneAt(pendingFork, "after-attempt-1", harness.DefaultNow().Add(-scenario.Day)), 1), "someone/Lg")
		for _, r := range []scenario.Run{a.Fork, a.MainSeptember, running} {
			Expect(env.Fake.AddRun(r)).To(Succeed())
		}
		Expect(env.Sync(ctx)).To(Succeed())

		for id, fork := range map[int64]bool{a.Fork.ID: true, a.MainSeptember.ID: false, pendingFork: true} {
			rows, err := index.IndexRun(runDir(env.Data(), id))
			Expect(err).NotTo(HaveOccurred())
			Expect(rows.Run.FromFork).To(Equal(fork), "run %d", id)
		}
	}, syncTimeout)
})

var _ = Describe("index.Open", Label("paths"), func() {
	It("drops and rebuilds an lg.db of format 1 with an older schema", func(ctx SpecContext) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
		reconcile(ctx, dbPath(env), env.Data())
		db := openDB(dbPath(env))
		_, err := db.Exec(`ALTER TABLE runs DROP COLUMN from_fork; DELETE FROM meta; INSERT INTO meta (format) VALUES (1);
			INSERT INTO runs (run_id, path) VALUES (99, 'stale')`)
		Expect(err).NotTo(HaveOccurred())

		reconcile(ctx, dbPath(env), env.Data())
		Expect(column[int64](db, "SELECT run_id FROM runs WHERE NOT from_fork")).To(Equal([]int64{runID}))
	}, syncTimeout)
})
