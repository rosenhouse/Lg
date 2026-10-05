package index_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/index"
)

var _ = Describe("the schema", Label("index"), func() {
	It("sets WAL, busy_timeout and meta.format 1", func() {
		path := filepath.Join(GinkgoT().TempDir(), "lg.db")
		ix, err := index.Open(path, GinkgoT().TempDir())
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
