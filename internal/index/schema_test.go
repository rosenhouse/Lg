package index_test

import (
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/index"
)

var _ = Describe("the schema", Label("index"), func() {
	It("sets WAL, busy_timeout and meta.format 1", func(ctx SpecContext) {
		path := filepath.Join(GinkgoT().TempDir(), "lg.db")
		ix, err := index.Open(ctx, path, GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)

		var timeout int
		Expect(ix.DB().QueryRow("PRAGMA busy_timeout").Scan(&timeout)).To(Succeed())
		Expect(timeout).To(BeNumerically(">=", 5000))
		db := openDB(path)
		Expect(column[string](db, "PRAGMA journal_mode")).To(Equal([]string{"wal"}))
		Expect(column[int](db, "SELECT format FROM meta")).To(Equal([]int{1}))
	})
})

var _ = Describe("index.Open", Label("index"), func() {
	It("keeps lg.db at path when path holds '?', '#' or '%'", func(ctx SpecContext) {
		dir := filepath.Join(GinkgoT().TempDir(), "a?b#c%3Fd")
		Expect(os.Mkdir(dir, 0o755)).To(Succeed())
		path := filepath.Join(dir, "lg.db")
		ix, err := index.Open(ctx, path, GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(ix.Close)

		Expect(path).To(BeARegularFile())
		var timeout int
		Expect(ix.DB().QueryRow("PRAGMA busy_timeout").Scan(&timeout)).To(Succeed())
		Expect(timeout).To(BeNumerically(">=", 5000))
	})

	It("succeeds when several open a fresh lg.db at once", func(ctx SpecContext) {
		for range 20 {
			path := filepath.Join(GinkgoT().TempDir(), "lg.db")
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					defer GinkgoRecover()
					ix, err := index.Open(ctx, path, GinkgoT().TempDir())
					Expect(err).NotTo(HaveOccurred())
					Expect(ix.Close()).To(Succeed())
				})
			}
			wg.Wait()
		}
	})
})
