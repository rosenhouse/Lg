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

const attemptPath = "data/github.com/o/r/runs/2026-10-03/1_ci_main/attempt-1"

var _ = Describe("store.Publish", Label("store"), func() {
	var root string

	BeforeEach(func() {
		root = newStore()
	})

	It("leaves data/ unchanged or holding the complete unit when filesystem op k and every later op fail, for every k, and a fresh Store's Sweep plus a retry then publishes", func() {
		reference := newStore()
		Expect(publishAttempt(open(reference), "{}")).To(Succeed())
		complete := files(filepath.Join(reference, "data"))
		dry := faultfs.New()
		Expect(publishAttempt(openFS(dry, newStore()), "{}")).To(Succeed())
		ops := len(dry.Journal())
		Expect(ops).To(BeNumerically(">=", 20))

		for k := 1; k <= ops; k++ {
			root := newStore()
			before := files(filepath.Join(root, "data"))
			faulty := faultfs.New()
			faulty.FailFrom(k, syscall.EIO)

			Expect(publishAttempt(openFS(faulty, root), "{}")).To(MatchError(syscall.EIO), "k=%d", k)
			Expect(files(filepath.Join(root, "data"))).To(Or(Equal(before), Equal(complete)), "k=%d", k)

			fresh := open(root)
			Expect(fresh.Sweep()).To(Succeed())
			Expect(os.ReadDir(filepath.Join(root, "tmp"))).To(BeEmpty(), "k=%d", k)
			Expect(publishAttempt(fresh, "{}")).To(Or(Succeed(), MatchError(store.ErrExists)), "k=%d", k)
			Expect(files(filepath.Join(root, "data"))).To(Equal(complete), "k=%d", k)
		}
	})

	It("writes and fsyncs each member, fsyncs the staging dir, renames it into data/ and fsyncs the parent, in that order", func() {
		journal := faultfs.New()
		Expect(publishAttempt(openFS(journal, root), "{}")).To(Succeed())

		var ops []string
		for _, op := range journal.Journal() {
			ops = append(ops, strings.ReplaceAll(op.String(), root+"/", ""))
		}
		Expect(ops).NotTo(BeEmpty())
		unit := strings.TrimPrefix(ops[0], "mkdir ")
		Expect(unit).To(HavePrefix("tmp/"))
		Expect(ops).To(HaveExactElements(
			"mkdir "+unit,
			"create "+unit+"/attempt.json",
			"write "+unit+"/attempt.json",
			"fsync "+unit+"/attempt.json",
			"close "+unit+"/attempt.json",
			"mkdir "+unit+"/jobs",
			"mkdir "+unit+"/jobs/1_build",
			"create "+unit+"/jobs/1_build/log.txt",
			"write "+unit+"/jobs/1_build/log.txt",
			"fsync "+unit+"/jobs/1_build/log.txt",
			"close "+unit+"/jobs/1_build/log.txt",
			"fsync "+unit+"/jobs/1_build",
			"fsync "+unit+"/jobs",
			"fsync "+unit,
			"mkdir data/github.com",
			"fsync data",
			"mkdir data/github.com/o",
			"fsync data/github.com",
			"mkdir data/github.com/o/r",
			"fsync data/github.com/o",
			"mkdir data/github.com/o/r/runs",
			"fsync data/github.com/o/r",
			"mkdir data/github.com/o/r/runs/2026-10-03",
			"fsync data/github.com/o/r/runs",
			"mkdir data/github.com/o/r/runs/2026-10-03/1_ci_main",
			"fsync data/github.com/o/r/runs/2026-10-03",
			"rename "+unit+" "+attemptPath,
			"fsync data/github.com/o/r/runs/2026-10-03/1_ci_main",
		))
	})

	It("returns ErrExists and leaves the target unchanged when the unit already exists", func() {
		s := open(root)
		Expect(publishAttempt(s, `{"first":true}`)).To(Succeed())
		before := treesnap.Snapshot(filepath.Join(root, attemptPath))

		Expect(publishAttempt(s, `{"second":true}`)).To(MatchError(store.ErrExists))
		Expect(treesnap.Snapshot(filepath.Join(root, attemptPath))).To(Equal(before))
	})
})

var _ = Describe("store.Open", Label("store"), func() {
	It("refuses a store whose tmp/ and data/ are on different devices", func() {
		root := newStore()
		Expect(store.OpenFS(faultfs.New(), root)).Error().NotTo(HaveOccurred())

		otherDevice := faultfs.New()
		otherDevice.SetMount(filepath.Join(root, "tmp"), store.Mount{Dev: 1 << 40})
		_, err := store.OpenFS(otherDevice, root)
		Expect(err).To(MatchError(And(
			ContainSubstring(filepath.Join(root, "tmp")),
			ContainSubstring(filepath.Join(root, "data")),
			ContainSubstring("different devices"))))
	})

	It("refuses a store whose tmp/ is another mount of data/'s device, as a bind mount is", func() {
		root := newStore()
		data, err := store.OSFS{}.Mount(filepath.Join(root, "data"))
		Expect(err).NotTo(HaveOccurred())

		bound := faultfs.New()
		bound.SetMount(filepath.Join(root, "tmp"), store.Mount{Dev: data.Dev, ID: data.ID + 1})
		_, err = store.OpenFS(bound, root)
		Expect(err).To(MatchError(ContainSubstring("different devices or mounts")))
	})
})

func newStore() string {
	GinkgoHelper()
	root := filepath.Join(GinkgoT().TempDir(), "lg")
	Expect(store.Init(root)).To(Succeed())
	return root
}

func open(root string) *store.Store {
	GinkgoHelper()
	s, err := store.Open(root)
	Expect(err).NotTo(HaveOccurred())
	return s
}

func openFS(fsys store.FS, root string) *store.Store {
	GinkgoHelper()
	s, err := store.OpenFS(fsys, root)
	Expect(err).NotTo(HaveOccurred())
	return s
}

// publishAttempt stages and publishes a unit of two members, one in a subdir.
func publishAttempt(s *store.Store, attemptJSON string) error {
	unit, err := s.NewUnit()
	if err != nil {
		return err
	}
	if err := unit.WriteJSON("attempt.json", []byte(attemptJSON)); err != nil {
		return err
	}
	w, err := unit.Create("jobs/1_build/log.txt")
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("log")); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return s.Publish(unit, filepath.Join(s.Data(), strings.TrimPrefix(attemptPath, "data/")))
}

// files maps each regular file under dir to its content. Dirs are left out,
// since an empty run dir may outlive a crash between mkdir and rename.
func files(dir string) map[string]string {
	GinkgoHelper()
	contents := map[string]string{}
	Expect(filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		content, err := os.ReadFile(path)
		contents[path[len(dir):]] = string(content)
		return err
	})).To(Succeed())
	return contents
}
