package store_test

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
)

var _ = Describe("store.Evict", Label("retention"), func() {
	var root, runDir, log string

	BeforeEach(func() {
		root = newStore()
		runDir = filepath.Join(root, "data", "github.com", "o", "r", "runs", "2026-06-01", "1_ci_main")
		log = filepath.Join(runDir, "attempt-1", "log.txt")
		Expect(os.MkdirAll(filepath.Dir(log), 0o755)).To(Succeed())
		Expect(os.WriteFile(log, []byte("old log\n"), 0o644)).To(Succeed())
	})

	It("renames the run dir into tmp/trash before deleting it, and an already-open file stays readable", func() {
		reader, err := os.Open(log)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(reader.Close)
		fsys := faultfs.New()

		Expect(openFS(fsys, root).Evict(runDir)).To(Succeed())
		Expect(runDir).NotTo(BeADirectory())
		journal := fsys.Journal()
		trash := filepath.Join(root, "tmp", "trash")
		renamed := slices.IndexFunc(journal, func(op faultfs.Op) bool {
			return op.Name == "rename" && op.Path == runDir && strings.HasPrefix(op.To, trash)
		})
		Expect(renamed).To(BeNumerically(">=", 0), "journal: %v", journal)
		removed := slices.IndexFunc(journal, func(op faultfs.Op) bool {
			return op.Name == "remove" && op.Path == journal[renamed].To
		})
		Expect(removed).To(BeNumerically(">", renamed), "journal: %v", journal)
		Expect(io.ReadAll(reader)).To(Equal([]byte("old log\n")))
		Expect(os.ReadDir(filepath.Join(root, "tmp"))).To(BeEmpty())
	})

	It("makes no dir, so it frees space on a full disk after a sweep", func() {
		fsys := faultfs.New()
		s := openFS(fsys, root)
		Expect(s.Sweep()).To(Succeed())
		fsys.FailOn("mkdir", syscall.ENOSPC)

		Expect(s.Evict(runDir)).To(Succeed())
		Expect(runDir).NotTo(BeADirectory())
	})
})
