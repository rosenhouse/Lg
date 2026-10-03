package store_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

var _ = Describe("Init", Label("store"), func() {
	var root string

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "lg")
	})

	It("creates FORMAT, .rgignore, data/, state/ and tmp/", func() {
		Expect(store.Init(root)).To(Succeed())

		Expect(os.ReadFile(filepath.Join(root, "FORMAT"))).To(Equal([]byte("lg-store 1\n")))
		Expect(os.ReadFile(filepath.Join(root, ".rgignore"))).To(Equal([]byte("state/\ntmp/\n")))
		for _, dir := range []string{"data", "state", "tmp"} {
			Expect(filepath.Join(root, dir)).To(BeADirectory())
		}
	})

	It("leaves a store that has a FORMAT alone", func() {
		Expect(os.MkdirAll(root, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "FORMAT"), []byte("lg-store 2\n"), 0o644)).To(Succeed())

		Expect(store.Init(root)).To(Succeed())
		entries, err := os.ReadDir(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveExactElements(HaveField("Name()", "FORMAT")))
		Expect(os.ReadFile(filepath.Join(root, "FORMAT"))).To(Equal([]byte("lg-store 2\n")))
	})

	It("keeps an edited .rgignore", func() {
		Expect(store.Init(root)).To(Succeed())
		Expect(os.Remove(filepath.Join(root, "FORMAT"))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, ".rgignore"), []byte("state/\n"), 0o644)).To(Succeed())

		Expect(store.Init(root)).To(Succeed())
		Expect(os.ReadFile(filepath.Join(root, ".rgignore"))).To(Equal([]byte("state/\n")))
		Expect(os.ReadFile(filepath.Join(root, "FORMAT"))).To(Equal([]byte("lg-store 1\n")))
	})
})

var _ = Describe("Open", Label("store"), func() {
	It("returns ErrFormat naming the FORMAT it found", func() {
		root := newStore()
		Expect(os.WriteFile(filepath.Join(root, "FORMAT"), []byte("lg-store 2\n"), 0o644)).To(Succeed())

		_, err := store.Open(root)
		Expect(err).To(MatchError(store.ErrFormat))
		Expect(err).To(MatchError(ContainSubstring(`"lg-store 2"`)))
	})

	It("returns fs.ErrNotExist for a dir with no FORMAT", func() {
		_, err := store.Open(GinkgoT().TempDir())
		Expect(err).To(MatchError(fs.ErrNotExist))
	})
})

var _ = Describe("Sweep", Label("store"), func() {
	It("empties tmp/ and leaves FORMAT, .rgignore, state/ and data/ untouched", func() {
		root := newStore()
		s := open(root)
		Expect(publishAttempt(s, "{}")).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "state", "status.json"), []byte("{}"), 0o644)).To(Succeed())
		unit, err := s.NewUnit()
		Expect(err).NotTo(HaveOccurred())
		Expect(unit.WriteJSON("jobs/1_build/job.json", []byte("{}"))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "tmp", "stray"), nil, 0o644)).To(Succeed())
		kept := treesnap.Snap{}
		for path, entry := range treesnap.Snapshot(root) {
			if !strings.HasPrefix(path, "tmp/") {
				kept[path] = entry
			}
		}

		Expect(s.Sweep()).To(Succeed())
		Expect(os.ReadDir(filepath.Join(root, "tmp"))).To(BeEmpty())
		Expect(treesnap.Snapshot(root)).To(treesnap.BeAppendOnlyFrom(kept))
	})
})

var _ = Describe("Publish", Label("store"), func() {
	It("maps EEXIST from renaming onto a non-empty dir to ErrExists", func() {
		root := newStore()
		Expect(publishAttempt(open(root), "{}")).To(Succeed())

		err := publishAttempt(open(root), "{}")
		Expect(err).To(MatchError(store.ErrExists))
		Expect(err).To(MatchError(ContainSubstring(attemptPath)))
	})

	It("maps ENOTEMPTY from rename to ErrExists", func() {
		dry := faultfs.New(store.OSFS{})
		Expect(publishAttempt(openFS(dry, newStore()), "{}")).To(Succeed())
		rename := -1
		for i, op := range dry.Journal() {
			if op.Name == "rename" {
				rename = i + 1
			}
		}
		notEmpty := faultfs.New(store.OSFS{})
		notEmpty.FailFrom(rename, syscall.ENOTEMPTY)

		Expect(publishAttempt(openFS(notEmpty, newStore()), "{}")).To(MatchError(store.ErrExists))
	})
})
