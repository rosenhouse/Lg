package store_test

import (
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/store"
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

	DescribeTable("keeps the old file, and removes the temp file, when a step fails",
		func(op string) {
			fsys := faultfs.New()
			fsys.FailOn(op, syscall.ENOSPC)

			Expect(openFS(fsys, newStore()).ReplaceFile(path, []byte("new"))).To(MatchError(syscall.ENOSPC))
			Expect(os.ReadFile(path)).To(Equal([]byte("old")))
			Expect(path + ".tmp").NotTo(BeAnExistingFile())
		},
		Entry("write", "write"),
		Entry("fsync", "fsync"),
		Entry("close", "close"),
		Entry("rename", "rename"),
	)
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

var _ = Describe("ReplaceFileUnlocked", Label("store"), func() {
	var dir, path string
	now := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		path = filepath.Join(dir, "f.json")
		Expect(os.WriteFile(path, []byte("old"), 0o644)).To(Succeed())
	})

	It("writes and fsyncs a temp file of its own, renames it over path and fsyncs the dir", func() {
		fsys := faultfs.New()
		Expect(store.ReplaceFileUnlocked(fsys, path, []byte("new"), now)).To(Succeed())

		Expect(os.ReadFile(path)).To(Equal([]byte("new")))
		journal := fsys.Journal()
		Expect(journal).To(HaveLen(6))
		tmp := journal[0].Path
		Expect(tmp).To(MatchRegexp(`^` + regexp.QuoteMeta(dir+"/.f.json.") + `\w+\.tmp$`))
		Expect(journal).To(Equal([]faultfs.Op{
			{Name: "create", Path: tmp},
			{Name: "write", Path: tmp},
			{Name: "fsync", Path: tmp},
			{Name: "close", Path: tmp},
			{Name: "rename", Path: tmp, To: path},
			{Name: "fsync", Path: dir},
		}))
	})

	It("stages each call in a different temp file", func() {
		temps := map[string]bool{}
		for range 2 {
			fsys := faultfs.New()
			Expect(store.ReplaceFileUnlocked(fsys, path, []byte("new"), now)).To(Succeed())
			temps[fsys.Journal()[0].Path] = true
		}
		Expect(temps).To(HaveLen(2))
	})

	It("keeps the old file, and removes the temp file, when a step fails", func() {
		fsys := faultfs.New()
		fsys.FailOn("rename", syscall.ENOSPC)

		Expect(store.ReplaceFileUnlocked(fsys, path, []byte("new"), now)).To(MatchError(syscall.ENOSPC))
		Expect(os.ReadFile(path)).To(Equal([]byte("old")))
		Expect(os.ReadDir(dir)).To(HaveLen(1))
	})

	It("removes the temp files that calls left over a minute ago, and keeps the others", func() {
		seed := func(name string, age time.Duration) string {
			p := filepath.Join(dir, name)
			Expect(os.WriteFile(p, []byte("x"), 0o644)).To(Succeed())
			Expect(os.Chtimes(p, now.Add(-age), now.Add(-age))).To(Succeed())
			return p
		}
		stale := seed(".f.json.abc.tmp", 2*time.Minute)
		live := seed(".f.json.def.tmp", 10*time.Second)
		others := []string{seed(".g.json.abc.tmp", 2*time.Minute), seed(".f.json.abc.bak", 2*time.Minute)}

		Expect(store.ReplaceFileUnlocked(faultfs.New(), path, []byte("new"), now)).To(Succeed())
		Expect(stale).NotTo(BeAnExistingFile())
		Expect(live).To(BeAnExistingFile())
		for _, o := range others {
			Expect(o).To(BeAnExistingFile())
		}
	})
})
