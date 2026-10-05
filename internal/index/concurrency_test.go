package index_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("index users sharing lg.db", Label("index"), func() {
	var env *harness.InProcessEnv

	BeforeEach(func(ctx SpecContext) {
		env = harness.InProcess()
		syncStages(ctx, env, "after-attempt-1")
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
		Expect(env.Sync(ctx)).To(Succeed())
	}, syncTimeout)

	It("open a current lg.db while another connection holds the write lock", func(ctx SpecContext) {
		reconcile(ctx, dbPath(env), env.Data())
		writer := impatientWriter(dbPath(env))
		tx, err := writer.BeginTx(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = tx.Rollback() })

		opened := make(chan error, 1)
		go func() {
			ix, err := index.Open(dbPath(env), env.Data())
			if err == nil {
				err = ix.Close()
			}
			opened <- err
		}()
		Eventually(opened).Should(Receive(Succeed()))
	}, syncTimeout)

	It("let another writer commit while Reconcile reads data/", func(ctx SpecContext) {
		ix, err := index.Open(dbPath(env), env.Data())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)
		waiting, release := stall(filepath.Join(layout.AttemptDir(runDir(env.Data(), runID), 1), "attempt.json"))
		reconciled := make(chan error, 1)
		go func() { reconciled <- ix.Reconcile(ctx) }()
		Eventually(waiting).Should(BeTrue())

		_, err = impatientWriter(dbPath(env)).ExecContext(ctx, "UPDATE meta SET format = format")
		release()
		Expect(err).NotTo(HaveOccurred())
		Eventually(reconciled).Should(Receive(Succeed()))
		Expect(count(openDB(dbPath(env)), "SELECT count(*) FROM runs")).To(Equal(2))
	}, syncTimeout)

	It("all succeed when several reconcile an unindexed store at once", func(ctx SpecContext) {
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				defer GinkgoRecover()
				ix, err := index.Open(dbPath(env), env.Data())
				Expect(err).NotTo(HaveOccurred())
				errs <- ix.Reconcile(ctx)
				Expect(ix.Close()).To(Succeed())
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(count(openDB(dbPath(env)), "SELECT count(*) FROM runs")).To(Equal(2))
	}, syncTimeout)

	It("drop a run that retention evicts while Reconcile reads data/", func(ctx SpecContext) {
		ix, err := index.Open(dbPath(env), env.Data())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)
		Expect(ix.Reconcile(ctx)).To(Succeed())
		for _, run := range []int64{runID, deletedRun} {
			Expect(os.Mkdir(filepath.Join(env.ArtifactDirs(run)[0], "extracted"), 0o755)).To(Succeed())
		}
		waiting, release := stall(filepath.Join(layout.AttemptDir(runDir(env.Data(), runID), 1), "attempt.json"))
		reconciled := make(chan error, 1)
		go func() { reconciled <- ix.Reconcile(ctx) }()
		Eventually(waiting).Should(BeTrue())

		Expect(os.Rename(runDir(env.Data(), deletedRun), filepath.Join(env.Tmp(), "evicted"))).To(Succeed())
		release()
		Eventually(reconciled).Should(Receive(Succeed()))
		Expect(column[int64](openDB(dbPath(env)), "SELECT DISTINCT run_id FROM jobs")).To(Equal([]int64{runID}))
	}, syncTimeout)
})

// impatientWriter opens path for writes that fail at once while another holds the write lock.
func impatientWriter(path string) *sql.DB {
	GinkgoHelper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(100)&_txlock=immediate")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(db.Close)
	return db
}

// stall replaces the file at path with a FIFO, so that a reader of path waits
// until release writes the file's content. waiting reports whether one does.
func stall(path string) (waiting func() bool, release func()) {
	GinkgoHelper()
	content, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.Remove(path)).To(Succeed())
	Expect(syscall.Mkfifo(path, 0o644)).To(Succeed())
	var fifo *os.File
	waiting = func() bool {
		if fifo == nil {
			fifo, _ = os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		}
		return fifo != nil
	}
	release = func() {
		GinkgoHelper()
		Expect(waiting()).To(BeTrue())
		Expect(fifo.Write(content)).To(Equal(len(content)))
		Expect(fifo.Close()).To(Succeed())
		Expect(os.Remove(path)).To(Succeed())
		Expect(os.WriteFile(path, content, 0o644)).To(Succeed())
	}
	return waiting, release
}
