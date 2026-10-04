package faultfs_test

import (
	"os"
	"path/filepath"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
)

var _ = Describe("FS", Label("store"), func() {
	var (
		dir string
		f   *faultfs.FS
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		f = faultfs.New()
	})

	// writeFile runs the five journaled ops of writing a file in a new dir.
	writeFile := func() error {
		if err := f.Mkdir(filepath.Join(dir, "d")); err != nil {
			return err
		}
		file, err := f.Create(filepath.Join(dir, "d", "a"))
		if err != nil {
			return err
		}
		if _, err := file.Write([]byte("x")); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		return file.Close()
	}

	It("passes ops through to the inner FS and journals the mutating ones", func() {
		Expect(writeFile()).To(Succeed())
		Expect(f.SyncDir(filepath.Join(dir, "d"))).To(Succeed())
		Expect(f.Rename(filepath.Join(dir, "d"), filepath.Join(dir, "e"))).To(Succeed())
		Expect(f.ReadDir(filepath.Join(dir, "e"))).To(HaveLen(1))
		Expect(f.Lstat(filepath.Join(dir, "e", "a"))).To(HaveField("Size()", BeEquivalentTo(1)))
		Expect(f.RemoveAll(filepath.Join(dir, "e"))).To(Succeed())

		Expect(filepath.Join(dir, "e")).NotTo(BeAnExistingFile())
		var journal []string
		for _, op := range f.Journal() {
			journal = append(journal, op.String())
		}
		Expect(journal).To(HaveExactElements(
			"mkdir "+dir+"/d",
			"create "+dir+"/d/a",
			"write "+dir+"/d/a",
			"fsync "+dir+"/d/a",
			"close "+dir+"/d/a",
			"fsync "+dir+"/d",
			"rename "+dir+"/d "+dir+"/e",
			"remove "+dir+"/e",
		))
	})

	DescribeTable("fails op k and every later op with the given error, leaving the disk as it was before op k",
		func(k int, onDisk []string) {
			f.FailFrom(k, syscall.ENOSPC)
			Expect(writeFile()).To(MatchError(syscall.ENOSPC))
			Expect(f.Mkdir(filepath.Join(dir, "later"))).To(MatchError(syscall.ENOSPC))

			Expect(f.Journal()).To(HaveLen(k - 1))
			var found []string
			Expect(filepath.Walk(dir, func(path string, _ os.FileInfo, err error) error {
				if path != dir {
					found = append(found, path[len(dir)+1:])
				}
				return err
			})).To(Succeed())
			Expect(found).To(Equal(onDisk))
		},
		Entry("mkdir", 1, nil),
		Entry("create", 2, []string{"d"}),
		Entry("write", 3, []string{"d", "d/a"}),
		Entry("fsync", 4, []string{"d", "d/a"}),
	)

	It("FailOn fails every op of the given name with the given error, leaving the others to run", Label("blocked"), func() {
		f.FailOn("write", syscall.ENOSPC)

		Expect(writeFile()).To(MatchError(syscall.ENOSPC))
		second, err := f.Create(filepath.Join(dir, "d", "b"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(second.Close)
		Expect(second.Write([]byte("x"))).Error().To(MatchError(syscall.ENOSPC))
		Expect(f.RemoveAll(filepath.Join(dir, "d"))).To(Succeed())
		Expect(filepath.Join(dir, "d")).NotTo(BeAnExistingFile())
		var journal []string
		for _, op := range f.Journal() {
			journal = append(journal, op.Name)
		}
		Expect(journal).To(Equal([]string{"mkdir", "create", "create", "remove"}))
	})

	It("FailOnUnder fails only the ops of the given name whose path or rename target is under dir", Label("blocked"), func() {
		Expect(os.Mkdir(filepath.Join(dir, "in"), 0o755)).To(Succeed())
		f.FailOnUnder("rename", filepath.Join(dir, "in"), syscall.EXDEV)

		Expect(writeFile()).To(Succeed())
		Expect(f.Rename(filepath.Join(dir, "d"), filepath.Join(dir, "in", "d"))).To(MatchError(syscall.EXDEV))
		Expect(f.Rename(filepath.Join(dir, "d"), filepath.Join(dir, "inside"))).To(Succeed())
		Expect(f.Rename(filepath.Join(dir, "inside"), filepath.Join(dir, "in"))).To(MatchError(syscall.EXDEV))
		Expect(f.Mkdir(filepath.Join(dir, "in", "e"))).To(Succeed())
	})

	It("reports the mount it was told for a path, and the inner FS's otherwise", func() {
		Expect(os.Mkdir(filepath.Join(dir, "a"), 0o755)).To(Succeed())
		real, err := store.OSFS{}.Mount(dir)
		Expect(err).NotTo(HaveOccurred())
		told := store.Mount{Dev: real.Dev + 1, ID: real.ID + 1}

		f.SetMount(filepath.Join(dir, "a"), told)
		Expect(f.Mount(filepath.Join(dir, "a"))).To(Equal(told))
		Expect(f.Mount(dir)).To(Equal(real))
	})
})
