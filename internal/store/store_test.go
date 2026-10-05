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
	"github.com/rosenhouse/lg/internal/testsupport/matchers"
	"github.com/rosenhouse/lg/internal/testsupport/treesnap"
)

var _ = Describe("Init", Label("store"), func() {
	var root string

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "lg")
	})

	It("creates FORMAT, .rgignore, data/, state/, tmp/ and tmp/trash/", func() {
		Expect(store.Init(root)).To(Succeed())

		Expect(os.ReadFile(filepath.Join(root, "FORMAT"))).To(Equal([]byte("lg-store 1\n")))
		Expect(os.ReadFile(filepath.Join(root, ".rgignore"))).To(Equal([]byte("/state/\n/tmp/\n")))
		for _, dir := range []string{"data", "state", "tmp", "tmp/trash"} {
			Expect(filepath.Join(root, dir)).To(BeADirectory())
		}
	})

	It("refuses a store with another FORMAT and leaves it alone", func() {
		Expect(os.MkdirAll(root, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "FORMAT"), []byte("lg-store 2\n"), 0o644)).To(Succeed())

		Expect(store.Init(root)).To(MatchError(store.ErrFormat))
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

	It("recreates a missing data/, state/ and tmp/ in an lg-store 1 store", func() {
		Expect(store.Init(root)).To(Succeed())
		for _, dir := range []string{"data", "state", "tmp"} {
			Expect(os.RemoveAll(filepath.Join(root, dir))).To(Succeed())
		}

		Expect(store.Init(root)).To(Succeed())
		Expect(store.Open(root)).Error().NotTo(HaveOccurred())
		Expect(filepath.Join(root, "state")).To(BeADirectory())
		Expect(filepath.Join(root, "tmp", "trash")).To(BeADirectory())
	})

	It("claims a root holding only what lg writes before FORMAT, and lg's config.yaml", func() {
		Expect(os.MkdirAll(filepath.Join(root, "data"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, "tmp", "unit-1"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "tmp", "unit-1", "FORMAT"), nil, 0o644)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, "tmp", "trash"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, "state"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "state", "write.lock"), []byte("1\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, ".rgignore"), []byte("state/\n"), 0o644)).To(Succeed())
		Expect(os.Mkdir(filepath.Join(root, "lost+found"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, ".DS_Store"), []byte("finder"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "config.yaml"), []byte("repo: o/r\n"), 0o644)).To(Succeed())

		Expect(store.Check(root)).To(Succeed())
		Expect(store.Init(root)).To(Succeed())
		Expect(os.ReadFile(filepath.Join(root, "FORMAT"))).To(Equal([]byte("lg-store 1\n")))
	})

	DescribeTable("refuses a root without FORMAT that holds files lg did not write, and leaves it untouched",
		func(path string) {
			Expect(os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(root, path), []byte("precious"), 0o644)).To(Succeed())
			before := treesnap.Snapshot(root)

			for _, err := range []error{store.Check(root), store.Init(root)} {
				Expect(err).To(MatchError(root + " has no FORMAT and holds files lg did not write; point LG_HOME at an empty or new dir"))
			}
			Expect(treesnap.Snapshot(root)).To(Equal(before))
		},
		Entry("a file at the top", "notes.txt"),
		Entry("a dir at the top", "Documents/notes.txt"),
		Entry("a file in data/", "data/notes.txt"),
		Entry("a file in state/ other than write.lock", "state/notes.txt"),
		Entry("a file in tmp/ that is not a unit", "tmp/project/notes.txt"),
		Entry("a file in lost+found/", "lost+found/#12"),
	)

	It("leaves no FORMAT or .rgignore, or a complete one, when filesystem op k and every later op fail, for every k, and a later Init and Open succeed", func() {
		dry := faultfs.New()
		Expect(store.InitFS(dry, filepath.Join(GinkgoT().TempDir(), "lg"))).To(Succeed())
		ops := len(dry.Journal())
		Expect(ops).To(BeNumerically(">=", 15))
		want := map[string]string{"FORMAT": "lg-store 1\n", ".rgignore": "/state/\n/tmp/\n"}

		for k := 1; k <= ops; k++ {
			root := filepath.Join(GinkgoT().TempDir(), "lg")
			faulty := faultfs.New()
			faulty.FailFrom(k, syscall.EIO)

			Expect(store.InitFS(faulty, root)).To(MatchError(syscall.EIO), "k=%d", k)
			for name, content := range want {
				got, err := os.ReadFile(filepath.Join(root, name))
				if err == nil {
					Expect(string(got)).To(Equal(content), "k=%d %s", k, name)
				} else {
					Expect(err).To(MatchError(fs.ErrNotExist), "k=%d %s", k, name)
				}
			}

			Expect(store.Init(root)).To(Succeed(), "k=%d", k)
			Expect(store.Open(root)).Error().NotTo(HaveOccurred(), "k=%d", k)
			for name, content := range want {
				Expect(os.ReadFile(filepath.Join(root, name))).To(Equal([]byte(content)), "k=%d %s", k, name)
			}
		}
	})

	// journal runs Init through faultfs and lists its ops, relative to root's parent.
	journal := func() []string {
		GinkgoHelper()
		fsys := faultfs.New()
		Expect(store.InitFS(fsys, root)).To(Succeed())
		var ops []string
		for _, op := range fsys.Journal() {
			ops = append(ops, strings.ReplaceAll(op.String(), filepath.Dir(root)+"/", ""))
		}
		return ops
	}

	It("makes dirs, fsyncs the root and its parent, then writes, fsyncs and renames .rgignore and then FORMAT into the root, fsyncing it after each", func() {
		Expect(os.MkdirAll(filepath.Join(root, "state"), 0o755)).To(Succeed())

		ops := journal()
		Expect(len(ops)).To(BeNumerically(">", 8))
		unit := strings.TrimPrefix(ops[8], "mkdir ")
		Expect(unit).To(HavePrefix("lg/tmp/unit-"))
		Expect(ops).To(HaveExactElements(
			"mkdir lg/data",
			"fsync lg",
			"mkdir lg/tmp",
			"fsync lg",
			"fsync lg",
			"fsync "+filepath.Dir(root),
			"mkdir lg/tmp/trash",
			"fsync lg/tmp",
			"mkdir "+unit,
			"create "+unit+"/.rgignore",
			"write "+unit+"/.rgignore",
			"fsync "+unit+"/.rgignore",
			"close "+unit+"/.rgignore",
			"rename "+unit+"/.rgignore lg/.rgignore",
			"fsync lg",
			"create "+unit+"/FORMAT",
			"write "+unit+"/FORMAT",
			"fsync "+unit+"/FORMAT",
			"close "+unit+"/FORMAT",
			"rename "+unit+"/FORMAT lg/FORMAT",
			"fsync lg",
			"remove "+unit,
		))
	})

	It("only fsyncs the root and its parent in a complete store", func() {
		Expect(store.Init(root)).To(Succeed())

		Expect(journal()).To(HaveExactElements("fsync lg", "fsync "+filepath.Dir(root)))
	})
})

var _ = Describe("MkdirAll", Label("store"), func() {
	It("makes each missing dir and then fsyncs its parent", func() {
		parent := GinkgoT().TempDir()
		journal := faultfs.New()

		Expect(store.MkdirAllFS(journal, filepath.Join(parent, "lg", "state"))).To(Succeed())
		Expect(filepath.Join(parent, "lg", "state")).To(BeADirectory())
		var ops []string
		for _, op := range journal.Journal() {
			ops = append(ops, op.String())
		}
		Expect(ops).To(HaveExactElements(
			"mkdir "+parent+"/lg",
			"fsync "+parent,
			"mkdir "+parent+"/lg/state",
			"fsync "+parent+"/lg",
		))
	})

	It("succeeds when another process makes a dir first", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "lg", "state")

		Expect(store.MkdirAllFS(racingFS{}, dir)).To(Succeed())
		Expect(dir).To(BeADirectory())
	})
})

// racingFS makes each dir just before it is asked to.
type racingFS struct{ store.OSFS }

func (f racingFS) Mkdir(path string) error {
	_ = f.OSFS.Mkdir(path)
	return f.OSFS.Mkdir(path)
}

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
	It("empties tmp/ and tmp/trash/, keeps tmp/trash/ so Evict needs no mkdir, and leaves FORMAT, .rgignore, state/ and data/ untouched", func() {
		root := newStore()
		s := open(root)
		Expect(publishAttempt(s, "{}")).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "state", "status.json"), []byte("{}"), 0o644)).To(Succeed())
		unit, err := s.NewUnit()
		Expect(err).NotTo(HaveOccurred())
		Expect(unit.WriteJSON("jobs/1_build/job.json", []byte("{}"))).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, "tmp", "trash", "x"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "tmp", "stray"), nil, 0o644)).To(Succeed())
		kept := treesnap.Snap{}
		for path, entry := range treesnap.Snapshot(root) {
			if !strings.HasPrefix(path, "tmp/") || path == "tmp/trash" {
				kept[path] = entry
			}
		}

		Expect(s.Sweep()).To(Succeed())
		Expect(treesnap.Snapshot(root)).To(Equal(kept))
	})

	It("empties tmp/ and tmp/trash/ of trees holding a dir without owner write permission", func() {
		root := newStore()
		writeReadOnlyDir(filepath.Join(root, "tmp", "unit-x"))
		writeReadOnlyDir(filepath.Join(root, "tmp", "trash", "y"))
		fsys := faultfs.New()
		fsys.ActAsNonRoot()

		Expect(openFS(fsys, root).Sweep()).To(Succeed())
		Expect(filepath.Join(root, "tmp")).To(matchers.BeSwept())
	})
})

var _ = Describe("Publish", Label("store"), func() {
	It("refuses a unit with an unclosed member, whose writes are not yet fsynced", func() {
		root := newStore()
		s := open(root)
		unit, err := s.NewUnit()
		Expect(err).NotTo(HaveOccurred())
		closed, err := unit.Create("jobs/1_build/job.json", store.Unlimited)
		Expect(err).NotTo(HaveOccurred())
		Expect(closed.Close()).To(Succeed())
		unclosed, err := unit.Create("jobs/1_build/log.txt", store.Unlimited)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(unclosed.Close)

		err = s.Publish(unit, filepath.Join(root, attemptPath))
		Expect(err).To(MatchError(`member "jobs/1_build/log.txt" is still open`))
		Expect(filepath.Join(root, attemptPath)).NotTo(BeAnExistingFile())
	})

	DescribeTable("refuses to publish through a symlink below data/, which retention would not follow",
		func(rel string) {
			root := newStore()
			outside := filepath.Join(GinkgoT().TempDir(), "outside")
			Expect(os.Mkdir(outside, 0o755)).To(Succeed())
			link := filepath.Join(root, "data", rel)
			Expect(os.MkdirAll(filepath.Dir(link), 0o755)).To(Succeed())
			Expect(os.Symlink(outside, link)).To(Succeed())

			Expect(publishAttempt(open(root), "{}")).To(MatchError(ContainSubstring(link)))
			Expect(os.ReadDir(outside)).To(BeEmpty())
		},
		Entry("host", "github.com"),
		Entry("repo", "github.com/o/r"),
		Entry("date", "github.com/o/r/runs/2026-10-03"),
	)

	It("maps EEXIST from renaming onto a non-empty dir to ErrExists", func() {
		root := newStore()
		Expect(publishAttempt(open(root), "{}")).To(Succeed())

		err := publishAttempt(open(root), "{}")
		Expect(err).To(MatchError(store.ErrExists))
		Expect(err).To(MatchError(ContainSubstring(attemptPath)))
	})

	It("maps ENOTEMPTY from rename to ErrExists", func() {
		dry := faultfs.New()
		Expect(publishAttempt(openFS(dry, newStore()), "{}")).To(Succeed())
		rename := -1
		for i, op := range dry.Journal() {
			if op.Name == "rename" {
				rename = i + 1
			}
		}
		notEmpty := faultfs.New()
		notEmpty.FailFrom(rename, syscall.ENOTEMPTY)

		Expect(publishAttempt(openFS(notEmpty, newStore()), "{}")).To(MatchError(store.ErrExists))
	})
})

var _ = Describe("Has", Label("store"), func() {
	It("reports whether a target exists, and an error when it cannot tell", func() {
		root := newStore()
		s := open(root)
		Expect(publishAttempt(s, "{}")).To(Succeed())
		attempt := filepath.Join(root, attemptPath)

		Expect(s.Has(attempt)).To(BeTrue())
		Expect(s.Has(filepath.Join(root, "data", "missing"))).To(BeFalse())
		_, err := s.Has(filepath.Join(attempt, "attempt.json", "child"))
		Expect(err).To(MatchError(syscall.ENOTDIR))
	})
})

var _ = DescribeTable("WriteJSON reports a member that failed to reach the disk, even when closing it succeeds", Label("store"),
	func(file failingFile) {
		s := openFS(failingFS{file: file}, newStore())
		unit, err := s.NewUnit()
		Expect(err).NotTo(HaveOccurred())

		Expect(unit.WriteJSON("a.json", []byte("{}"))).To(MatchError(syscall.EIO))
	},
	Entry("write", failingFile{write: syscall.EIO}),
	Entry("fsync", failingFile{sync: syscall.EIO}),
)

// failingFS creates files whose Write or Sync fails and whose Close succeeds.
type failingFS struct {
	store.OSFS
	file failingFile
}

func (f failingFS) Create(path string) (store.File, error) {
	file, err := f.OSFS.Create(path)
	if err != nil {
		return nil, err
	}
	DeferCleanup(file.Close)
	return f.file, nil
}

type failingFile struct{ write, sync error }

func (f failingFile) Write(p []byte) (int, error) {
	if f.write != nil {
		return 0, f.write
	}
	return len(p), nil
}

func (f failingFile) Sync() error { return f.sync }

func (f failingFile) Close() error { return nil }

var _ = Describe("FindRunDir", Label("attempts"), func() {
	var (
		s    *store.Store
		date string
	)

	BeforeEach(func() {
		root := filepath.Join(GinkgoT().TempDir(), "lg")
		Expect(store.Init(root)).To(Succeed())
		var err error
		s, err = store.Open(root)
		Expect(err).NotTo(HaveOccurred())
		date = filepath.Join(s.Data(), "github.com/rosenhouse/Lg/runs/2026-10-03")
	})

	It("finds the dir of the run's id in the date dir whatever its slugs", func() {
		Expect(os.MkdirAll(filepath.Join(date, "77_lg-fixture_main"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(date, "7_old-name_main"), 0o755)).To(Succeed())

		Expect(s.FindRunDir(filepath.Join(date, "7_new-name_other"))).To(Equal(filepath.Join(date, "7_old-name_main")))
	})

	It("gives the dir it is asked for when it exists, without reading the date dir", func() {
		Expect(os.MkdirAll(filepath.Join(date, "7_lg-fixture_main"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(date, "7_old-name_main"), 0o755)).To(Succeed())
		unreadable, err := store.OpenFS(unreadableDir{path: date}, filepath.Dir(s.Data()))
		Expect(err).NotTo(HaveOccurred())

		Expect(unreadable.FindRunDir(filepath.Join(date, "7_lg-fixture_main"))).To(Equal(filepath.Join(date, "7_lg-fixture_main")))
	})

	It("gives the dir it is asked for when the date dir holds none of the run's id", func() {
		Expect(os.MkdirAll(filepath.Join(date, "77_lg-fixture_main"), 0o755)).To(Succeed())

		Expect(s.FindRunDir(filepath.Join(date, "7_lg-fixture_main"))).To(Equal(filepath.Join(date, "7_lg-fixture_main")))
	})

	It("gives the dir it is asked for when the date dir is missing", func() {
		Expect(s.FindRunDir(filepath.Join(date, "7_lg-fixture_main"))).To(Equal(filepath.Join(date, "7_lg-fixture_main")))
	})

	It("returns the error when the date dir cannot be read", func() {
		Expect(os.MkdirAll(filepath.Join(date, "7_old-name_main"), 0o755)).To(Succeed())
		unreadable, err := store.OpenFS(unreadableDir{path: date}, filepath.Dir(s.Data()))
		Expect(err).NotTo(HaveOccurred())

		Expect(unreadable.FindRunDir(filepath.Join(date, "7_new-name_main"))).Error().To(MatchError(syscall.EIO))
	})

	It("returns the error when the run dir cannot be checked", func() {
		Expect(os.MkdirAll(filepath.Dir(date), 0o755)).To(Succeed())
		Expect(os.WriteFile(date, nil, 0o644)).To(Succeed())

		Expect(s.FindRunDir(filepath.Join(date, "7_lg-fixture_main"))).Error().To(MatchError(syscall.ENOTDIR))
	})
})

var _ = Describe("Names", Label("attempts"), func() {
	var (
		s   *store.Store
		dir string
	)

	BeforeEach(func() {
		root := filepath.Join(GinkgoT().TempDir(), "lg")
		Expect(store.Init(root)).To(Succeed())
		var err error
		s, err = store.Open(root)
		Expect(err).NotTo(HaveOccurred())
		dir = filepath.Join(s.Data(), "run")
	})

	It("lists the names in a dir", func() {
		Expect(os.MkdirAll(filepath.Join(dir, "attempt-1"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "x"), nil, 0o644)).To(Succeed())

		Expect(s.Names(dir)).To(ConsistOf("attempt-1", "x"))
	})

	It("lists none in a missing dir", func() {
		Expect(s.Names(dir)).To(BeEmpty())
	})

	It("returns the error when the dir cannot be read", func() {
		Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
		unreadable, err := store.OpenFS(unreadableDir{path: dir}, filepath.Dir(s.Data()))
		Expect(err).NotTo(HaveOccurred())

		Expect(unreadable.Names(dir)).Error().To(MatchError(syscall.EIO))
	})
})

// unreadableDir fails ReadDir of path.
type unreadableDir struct {
	store.OSFS
	path string
}

func (u unreadableDir) ReadDir(path string) ([]fs.DirEntry, error) {
	if filepath.Clean(path) == u.path {
		return nil, syscall.EIO
	}
	return u.OSFS.ReadDir(path)
}
