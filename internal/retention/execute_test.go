package retention_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/retention"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
)

var _ = Describe("retention.Execute", Label("retention"), func() {
	var (
		root    string
		fsys    *faultfs.FS
		s       *store.Store
		runs    string
		removed []string
	)

	record := func(dir string) { removed = append(removed, dir) }

	// runWithLog writes a run dir holding a log, and gives its path.
	runWithLog := func(rel string) string {
		GinkgoHelper()
		dir := filepath.Join(runs, rel)
		writeSized(dir, "attempt-1/log.txt", 10)
		return dir
	}

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "lg")
		Expect(store.Init(root)).To(Succeed())
		fsys = faultfs.New()
		var err error
		s, err = store.OpenFS(fsys, root)
		Expect(err).NotTo(HaveOccurred())
		runs = filepath.Join(s.Data(), "github.com", "o", "r", "runs")
		removed = nil
	})

	It("evicts each victim in order, reports it, and removes the date dirs left empty", func() {
		expired := runWithLog("2026-06-01/1_ci_main")
		kept := runWithLog("2026-10-02/3_ci_main")
		extracted := filepath.Join(kept, "artifacts", "5_report", "extracted")
		writeSized(extracted, "report.xml", 10)
		evicted := runWithLog("2026-10-01/2_ci_main")
		leftover := filepath.Join(s.Data(), "ghe.example.com", "a", "b", "runs", "2026-05-01")
		Expect(os.MkdirAll(leftover, 0o755)).To(Succeed())

		v := retention.Victims{Expired: []string{expired}, Extracted: []string{extracted}, Evicted: []string{evicted}}
		Expect(retention.Execute(context.Background(), s, v, record)).To(Succeed())
		Expect(removed).To(Equal(v.Dirs()))
		var trashed []string
		for _, op := range fsys.Journal() {
			if op.Name == "rename" && filepath.Dir(op.To) == filepath.Join(root, "tmp", "trash") {
				trashed = append(trashed, op.Path)
			}
		}
		Expect(trashed).To(Equal(v.Dirs()))
		Expect(filepath.Dir(expired)).NotTo(BeADirectory())
		Expect(filepath.Dir(evicted)).NotTo(BeADirectory())
		Expect(leftover).NotTo(BeADirectory())
		Expect(filepath.Join(kept, "attempt-1", "log.txt")).To(BeARegularFile())
	})

	It("stops between evictions once its context is done", func() {
		first := runWithLog("2026-06-01/1_ci_main")
		second := runWithLog("2026-06-01/2_ci_main")
		ctx, cancel := context.WithCancel(context.Background())
		cancelling := func(string) { cancel() }

		err := retention.Execute(ctx, s, retention.Victims{Expired: []string{first, second}}, cancelling)
		Expect(err).To(MatchError(context.Canceled))
		Expect(first).NotTo(BeADirectory())
		Expect(second).To(BeADirectory())
	})

	It("removes a date dir that holds only Finder's .DS_Store", func() {
		writeSized(runs, "2026-06-01/.DS_Store", 1)

		Expect(retention.Execute(context.Background(), s, retention.Victims{}, record)).To(Succeed())
		Expect(filepath.Join(runs, "2026-06-01")).NotTo(BeAnExistingFile())
	})

	It("leaves an empty dir under runs/ whose name is not a date", func() {
		notes := filepath.Join(runs, "notes")
		Expect(os.MkdirAll(notes, 0o755)).To(Succeed())

		Expect(retention.Execute(context.Background(), s, retention.Victims{}, record)).To(Succeed())
		Expect(notes).To(BeADirectory())
	})

	It("removes no empty date dir through a symlink", func() {
		outside := filepath.Join(GinkgoT().TempDir(), "outside")
		empty := filepath.Join(outside, "owner", "repo", "runs", "2026-01-01")
		Expect(os.MkdirAll(empty, 0o755)).To(Succeed())
		Expect(os.Symlink(outside, filepath.Join(s.Data(), "linkedhost"))).To(Succeed())

		Expect(retention.Execute(context.Background(), s, retention.Victims{}, record)).To(Succeed())
		Expect(empty).To(BeADirectory())
	})

	It("leaves a file among the date dirs alone", func() {
		writeSized(runs, "notes.txt", 1)

		Expect(retention.Execute(context.Background(), s, retention.Victims{}, record)).To(Succeed())
		Expect(filepath.Join(runs, "notes.txt")).To(BeARegularFile())
	})

	// renamed gives the index of the rename of path in the journal.
	renamed := func(path string) int {
		return slices.IndexFunc(fsys.Journal(), func(op faultfs.Op) bool { return op.Name == "rename" && op.Path == path })
	}

	It("writes the victims' horizon after the expired runs and extracted trees go and before any evicted run", func() {
		expired := runWithLog("2026-06-01/1_ci_main")
		extracted := filepath.Join(runs, "2026-10-02", "3_ci_main", "artifacts", "5_report", "extracted")
		writeSized(extracted, "report.xml", 10)
		older := runWithLog("2026-10-01/9_ci_main")
		newer := runWithLog("2026-10-01/10_ci_main")

		v := retention.Victims{Expired: []string{expired}, Extracted: []string{extracted}, Evicted: []string{newer, older}, Horizons: retention.Horizons{"github.com/o/r": time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}}
		Expect(retention.Execute(context.Background(), s, v, record)).To(Succeed())
		h, _, err := retention.ReadHorizons(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(h).To(Equal(retention.Horizons{"github.com/o/r": time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}))
		horizon := renamed(filepath.Join(s.State(), "horizon.json.tmp"))
		Expect(renamed(expired)).To(BeNumerically("<", renamed(extracted)))
		Expect(renamed(extracted)).To(BeNumerically("<", horizon))
		Expect(horizon).To(BeNumerically("<", renamed(newer)))
	})

	DescribeTable("on a full disk, evicts runs for disk_cap until it can write the horizon",
		func(full syscall.Errno) {
			expired := runWithLog("2026-06-01/1_ci_main")
			first := runWithLog("2026-10-01/9_ci_main")
			second := runWithLog("2026-10-01/10_ci_main")
			fsys.FailOnUnder("create", filepath.Join(s.State(), "horizon.json.tmp"), full)

			err := retention.Execute(context.Background(), s, retention.Victims{Expired: []string{expired}, Evicted: []string{first, second}, Horizons: retention.Horizons{"github.com/o/r": time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)}}, record)
			Expect(err).To(MatchError(full))
			for _, dir := range []string{expired, first, second} {
				Expect(dir).NotTo(BeADirectory())
			}
			Expect(removed).To(Equal([]string{expired, first, second}))
		},
		Entry("ENOSPC", syscall.ENOSPC),
		Entry("EDQUOT", syscall.EDQUOT),
	)

	It("writes no horizon when the victims raise none", func() {
		evicted := runWithLog("2026-10-01/9_ci_main")

		Expect(retention.Execute(context.Background(), s, retention.Victims{Evicted: []string{evicted}}, record)).To(Succeed())
		Expect(evicted).NotTo(BeADirectory())
		Expect(filepath.Join(s.State(), "horizon.json")).NotTo(BeAnExistingFile())
	})
})

var _ = Describe("retention.Retain", Label("retention"), func() {
	var (
		s       *store.Store
		runs    string
		removed []string
		now     = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	)

	record := func(dir string) { removed = append(removed, dir) }

	// runOf writes a run dir of 65 bytes created at createdAt.
	runOf := func(rel string, createdAt time.Time) string {
		GinkgoHelper()
		dir := filepath.Join(runs, rel)
		writeSized(dir, "attempt-1/log.txt", 10)
		writeFetch(filepath.Join(dir, "attempt-1"), createdAt)
		return dir
	}

	BeforeEach(func() {
		s = newStore()
		runs = filepath.Join(s.Data(), "github.com", "o", "r", "runs")
		removed = nil
	})

	It("evicts a kept run at or before the stored horizon, under disk_cap too", func() {
		at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		Expect(retention.Horizons{"github.com/o/r": at}.Write(s)).To(Succeed())
		passed := runOf("2026-10-01/9_ci_main", at)
		kept := runOf("2026-10-01/10_ci_main", at.Add(time.Second))

		Expect(retention.Retain(context.Background(), s, now, 90*24*time.Hour, 1<<30, record)).To(Succeed())
		Expect(removed).To(Equal([]string{passed}))
		Expect(kept).To(BeADirectory())
	})

	It("counts only run dirs toward disk_cap, since it can evict nothing else", func() {
		writeSized(s.Data(), "github.com/o/r/notes.txt", 1000)
		kept := runOf("2026-10-01/9_ci_main", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

		Expect(retention.Retain(context.Background(), s, now, 90*24*time.Hour, 100, record)).To(Succeed())
		Expect(removed).To(BeEmpty())
		Expect(kept).To(BeADirectory())
	})

	It("moves a corrupt horizon aside, evicts, writes the raised horizon, and then reports it", func() {
		path := filepath.Join(s.State(), "horizon.json")
		Expect(os.WriteFile(path, []byte("{"), 0o644)).To(Succeed())
		expired := runOf("2026-06-01/1_ci_main", time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC))
		evicted := runOf("2026-10-01/9_ci_main", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
		kept := runOf("2026-10-02/10_ci_main", time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))

		err := retention.Retain(context.Background(), s, now, 90*24*time.Hour, 100, record)
		Expect(err).To(MatchError(ContainSubstring(path)))
		Expect(expired).NotTo(BeADirectory())
		Expect(evicted).NotTo(BeADirectory())
		Expect(kept).To(BeADirectory())
		Expect(os.ReadFile(path + ".corrupt")).To(Equal([]byte("{")))
		h, _, err := retention.ReadHorizons(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(h).To(Equal(retention.Horizons{"github.com/o/r": time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}))
	})
})
