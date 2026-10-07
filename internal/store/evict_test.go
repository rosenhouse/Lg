package store_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
	"github.com/rosenhouse/lg/internal/testsupport/matchers"
)

var _ = Describe("Evict", Label("retention"), func() {
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
			return op.Name == "rename" && op.Path == runDir && filepath.Dir(op.To) == trash
		})
		Expect(renamed).To(BeNumerically(">=", 0), "journal: %v", journal)
		removed := slices.IndexFunc(journal, func(op faultfs.Op) bool {
			return op.Name == "remove" && op.Path == journal[renamed].To
		})
		Expect(removed).To(BeNumerically(">", renamed), "journal: %v", journal)
		Expect(io.ReadAll(reader)).To(Equal([]byte("old log\n")))
		Expect(filepath.Join(root, "tmp")).To(matchers.BeSwept())
	})

	It("removes a tree holding a dir without owner write permission, as an extracted archive may", func() {
		writeReadOnlyDir(filepath.Join(runDir, "artifacts", "5_report", "extracted"))
		fsys := faultfs.New()
		fsys.ActAsNonRoot()

		Expect(openFS(fsys, root).Evict(runDir)).To(Succeed())
		Expect(filepath.Join(root, "tmp")).To(matchers.BeSwept())
	})

	It("removes a tree holding a read-only dir when the first remove fails with an error other than EACCES", func() {
		writeReadOnlyDir(filepath.Join(runDir, "artifacts", "5_report", "extracted"))
		fsys := faultfs.New()
		fsys.FailRemoveOfReadOnly(errors.New("symlink in a read-only dir"))

		Expect(openFS(fsys, root).Evict(runDir)).To(Succeed())
		Expect(filepath.Join(root, "tmp")).To(matchers.BeSwept())
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

// writeReadOnlyDir writes dir/ro/file and takes write permission from dir/ro.
func writeReadOnlyDir(dir string) {
	GinkgoHelper()
	readOnly := filepath.Join(dir, "ro")
	Expect(os.MkdirAll(readOnly, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(readOnly, "file"), nil, 0o644)).To(Succeed())
	Expect(os.Chmod(readOnly, 0o555)).To(Succeed())
}
