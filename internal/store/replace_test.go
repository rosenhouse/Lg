package store_test

import (
	"os"
	"path/filepath"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
)

var _ = Describe("ReplaceFile", Label("store"), func() {
	var dir, path string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		path = filepath.Join(dir, "f.json")
		Expect(os.WriteFile(path, []byte("old"), 0o644)).To(Succeed())
	})

	It("writes and fsyncs a temp file, renames it over path and fsyncs the dir", func() {
		fsys := faultfs.New()
		Expect(openFS(fsys, newStore()).ReplaceFile(path, []byte("new"))).To(Succeed())

		Expect(os.ReadFile(path)).To(Equal([]byte("new")))
		tmp := path + ".tmp"
		Expect(fsys.Journal()).To(Equal([]faultfs.Op{
			{Name: "remove", Path: tmp},
			{Name: "create", Path: tmp},
			{Name: "write", Path: tmp},
			{Name: "fsync", Path: tmp},
			{Name: "close", Path: tmp},
			{Name: "rename", Path: tmp, To: path},
			{Name: "fsync", Path: dir},
		}))
	})

	It("replaces a temp file that a killed writer left", func() {
		Expect(os.WriteFile(path+".tmp", []byte("stale"), 0o644)).To(Succeed())

		Expect(open(newStore()).ReplaceFile(path, []byte("new"))).To(Succeed())
		Expect(os.ReadFile(path)).To(Equal([]byte("new")))
		Expect(path + ".tmp").NotTo(BeAnExistingFile())
	})

	It("keeps the old file when the write fails", func() {
		fsys := faultfs.New()
		fsys.FailOn("fsync", syscall.EIO)

		Expect(openFS(fsys, newStore()).ReplaceFile(path, []byte("new"))).To(MatchError(syscall.EIO))
		Expect(os.ReadFile(path)).To(Equal([]byte("old")))
	})
})

var _ = Describe("Rename", Label("store"), func() {
	It("renames oldpath to newpath and fsyncs newpath's dir", func() {
		root := newStore()
		path := filepath.Join(root, "state", "f.json")
		Expect(os.WriteFile(path, []byte("old"), 0o644)).To(Succeed())
		fsys := faultfs.New()

		Expect(openFS(fsys, root).Rename(path, path+".corrupt")).To(Succeed())
		Expect(os.ReadFile(path + ".corrupt")).To(Equal([]byte("old")))
		Expect(fsys.Journal()).To(Equal([]faultfs.Op{
			{Name: "rename", Path: path, To: path + ".corrupt"},
			{Name: "fsync", Path: filepath.Join(root, "state")},
		}))
	})
})

var _ = Describe("State", Label("store"), func() {
	It("is the store's state/ dir", func() {
		root := newStore()
		Expect(open(root).State()).To(Equal(filepath.Join(root, "state")))
	})
})
