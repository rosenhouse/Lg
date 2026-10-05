package index_test

import (
	"bytes"
	"context"
	"database/sql"
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

	It("drops a table whose name holds a quote and a backslash", func(ctx SpecContext) {
		reconcile(ctx, dbPath(env), env.Data())
		_, err := openDB(dbPath(env)).ExecContext(ctx, `CREATE TABLE "a""b\c" (x)`)
		Expect(err).NotTo(HaveOccurred())

		Expect(index.Rebuild(ctx, dbPath(env), env.Data())).To(Succeed())
		Expect(column[string](openDB(dbPath(env)), "SELECT name FROM sqlite_master WHERE name = ?", `a"b\c`)).To(BeEmpty())
	})

	It("empties lg.db when data/ holds no runs", func(ctx SpecContext) {
		reconcile(ctx, dbPath(env), env.Data())
		Expect(os.RemoveAll(runDir(env.Data(), runID))).To(Succeed())

		Expect(index.Rebuild(ctx, dbPath(env), env.Data())).To(Succeed())
		Expect(count(openDB(dbPath(env)), "SELECT count(*) FROM runs")).To(Equal(0))
	})
})

var _ = DescribeTable("starting from empty over a file SQLite cannot read", Label("index"),
	func(ctx SpecContext, damage func(path string), indexAgain func(ctx context.Context, path, data string)) {
		env := harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
		reconcile(ctx, dbPath(env), env.Data())
		damage(dbPath(env))

		indexAgain(ctx, dbPath(env), env.Data())
		db := openDB(dbPath(env))
		Expect(count(db, "SELECT count(*) FROM jobs")).To(Equal(12))
		Expect(column[string](db, "SELECT workflow_name FROM runs")).To(Equal([]string{"lg-fixture"}))
	},
	Entry("index.Open, over a file that is not a db", syncTimeout, notADB, reconcile),
	Entry("index.Open, over a db with corrupt pages", syncTimeout, corruptPages, reconcile),
	Entry("index.Open, over a file that is not a db beside a stale WAL", syncTimeout, notADBBesideAStaleWAL, reconcile),
	Entry("index.Rebuild, over a file that is not a db", syncTimeout, notADB, rebuild),
	Entry("index.Rebuild, over a db with corrupt pages", syncTimeout, corruptPages, rebuild),
	Entry("index.Rebuild, over a db whose jobs table has a corrupt page", syncTimeout, corruptRootPage("jobs"), rebuild),
	Entry("index.Reconcile, over a db whose index of unit paths has a corrupt page", syncTimeout, corruptRootPage("units_path"), reconcile),
)

func notADB(path string) {
	GinkgoHelper()
	Expect(os.WriteFile(path, []byte("not a db, but long enough for SQLite to read its header"), 0o644)).To(Succeed())
}

// notADBBesideAStaleWAL leaves the WAL of a db that renamed the workflow.
func notADBBesideAStaleWAL(path string) {
	GinkgoHelper()
	db := openDB(path)
	_, err := db.ExecContext(context.Background(), "UPDATE runs SET workflow_name = 'stale'")
	Expect(err).NotTo(HaveOccurred())
	wal, err := os.ReadFile(path + "-wal")
	Expect(err).NotTo(HaveOccurred())
	Expect(db.Close()).To(Succeed())
	notADB(path)
	Expect(os.WriteFile(path+"-wal", wal, 0o644)).To(Succeed())
}

// corruptPages overwrites every page after the first, which holds the schema.
func corruptPages(path string) {
	GinkgoHelper()
	info, err := os.Stat(path)
	Expect(err).NotTo(HaveOccurred())
	const pageSize = 4096
	garbage := bytes.Repeat([]byte{0x5a}, int(info.Size())-pageSize)
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	Expect(err).NotTo(HaveOccurred())
	Expect(f.WriteAt(garbage, pageSize)).To(Equal(len(garbage)))
	Expect(f.Close()).To(Succeed())
}

// corruptRootPage overwrites the root page of a table or index, leaving meta readable.
func corruptRootPage(name string) func(path string) {
	return func(path string) {
		GinkgoHelper()
		db, err := sql.Open("sqlite", path)
		Expect(err).NotTo(HaveOccurred())
		var page, pageSize int64
		Expect(db.QueryRowContext(context.Background(), "SELECT rootpage FROM sqlite_master WHERE name = ?", name).Scan(&page)).To(Succeed())
		Expect(db.QueryRowContext(context.Background(), "PRAGMA page_size").Scan(&pageSize)).To(Succeed())
		Expect(db.Close()).To(Succeed())
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.WriteAt(bytes.Repeat([]byte{0x5a}, int(pageSize)), (page-1)*pageSize)).To(Equal(int(pageSize)))
		Expect(f.Close()).To(Succeed())
	}
}

func rebuild(ctx context.Context, path, data string) {
	GinkgoHelper()
	Expect(index.Rebuild(ctx, path, data)).To(Succeed())
}

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
