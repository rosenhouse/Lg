package index_test

import (
	"errors"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("index.Rebuild", Label("index"), func() {
	var env *harness.InProcessEnv

	BeforeEach(func(ctx SpecContext) {
		env = harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
	}, syncTimeout)

	It("starts from empty over a file that is not an SQLite db", func(ctx SpecContext) {
		Expect(os.WriteFile(dbPath(env), []byte("not a db, but long enough for SQLite to read its header"), 0o644)).To(Succeed())
		Expect(index.Open(ctx, dbPath(env), env.Data())).Error().To(MatchError(ContainSubstring(dbPath(env))))

		Expect(index.Rebuild(ctx, dbPath(env), env.Data())).To(Succeed())
		Expect(count(openDB(dbPath(env)), "SELECT count(*) FROM jobs")).To(Equal(12))
	})

	It("starts from empty over a schema that lost a table or gained a column", func(ctx SpecContext) {
		reconcile(ctx, dbPath(env), env.Data())
		_, err := openDB(dbPath(env)).ExecContext(ctx, "DROP TABLE steps; ALTER TABLE jobs ADD COLUMN note TEXT")
		Expect(err).NotTo(HaveOccurred())

		Expect(index.Rebuild(ctx, dbPath(env), env.Data())).To(Succeed())
		db := openDB(dbPath(env))
		Expect(count(db, "SELECT count(*) FROM jobs")).To(Equal(12))
		Expect(count(db, "SELECT count(*) FROM steps")).To(BeNumerically(">", 0))
		Expect(column[string](db, "SELECT name FROM pragma_table_info('jobs') WHERE name = 'note'")).To(BeEmpty())
	})

	It("empties lg.db when data/ holds no runs", func(ctx SpecContext) {
		reconcile(ctx, dbPath(env), env.Data())
		Expect(os.RemoveAll(runDir(env.Data(), runID))).To(Succeed())

		Expect(index.Rebuild(ctx, dbPath(env), env.Data())).To(Succeed())
		Expect(count(openDB(dbPath(env)), "SELECT count(*) FROM runs")).To(Equal(0))
	})
})

var _ = Describe("an index transaction", Label("index"), func() {
	It("fails with only the cause when SQLite has already rolled it back", func(ctx SpecContext) {
		ix, err := index.Open(ctx, dbPath(harness.InProcess()), GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)
		full := errors.New("database or disk is full")

		Expect(ix.InTx(ctx, func(tx index.Tx) error {
			_, err := tx.ExecContext(ctx, "ROLLBACK")
			Expect(err).NotTo(HaveOccurred())
			return full
		})).To(MatchError(full.Error()))
	})
})
