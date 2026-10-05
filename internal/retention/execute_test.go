package retention_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		root string
		fsys *faultfs.FS
		s    *store.Store
		runs string
		out  bytes.Buffer
	)

	// runWithLog writes a run dir holding a log, and gives it as created at createdAt.
	runWithLog := func(rel string, createdAt time.Time) retention.Run {
		GinkgoHelper()
		dir := filepath.Join(runs, rel)
		writeSized(dir, "attempt-1/log.txt", 10)
		date, _, _ := strings.Cut(rel, "/")
		return retention.Run{Dir: dir, Date: date, CreatedAt: createdAt}
	}

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "lg")
		Expect(store.Init(root)).To(Succeed())
		fsys = faultfs.New()
		var err error
		s, err = store.OpenFS(fsys, root)
		Expect(err).NotTo(HaveOccurred())
		runs = filepath.Join(s.Data(), "github.com", "o", "r", "runs")
		out.Reset()
	})

	It("evicts each victim in order, prints it, and removes the date dirs left empty", func() {
		expired := runWithLog("2026-06-01/1_ci_main", time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC))
		kept := runWithLog("2026-10-02/3_ci_main", time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC))
		extracted := filepath.Join(kept.Dir, "artifacts", "5_report", "extracted")
		writeSized(extracted, "report.xml", 10)
		evicted := runWithLog("2026-10-01/2_ci_main", time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC))
		leftover := filepath.Join(s.Data(), "ghe.example.com", "a", "b", "runs", "2026-05-01")
		Expect(os.MkdirAll(leftover, 0o755)).To(Succeed())

		v := retention.Victims{Expired: []string{expired.Dir}, Extracted: []string{extracted}, Evicted: []retention.Run{evicted}}
		Expect(retention.Execute(s, v, &out)).To(Succeed())
		Expect(out.String()).To(Equal(strings.Join(v.Dirs(), "\n") + "\n"))
		var trashed []string
		for _, op := range fsys.Journal() {
			if op.Name == "rename" && strings.HasPrefix(op.To, filepath.Join(root, "tmp", "trash")) {
				trashed = append(trashed, op.Path)
			}
		}
		Expect(trashed).To(Equal(v.Dirs()))
		Expect(filepath.Dir(expired.Dir)).NotTo(BeADirectory())
		Expect(filepath.Dir(evicted.Dir)).NotTo(BeADirectory())
		Expect(leftover).NotTo(BeADirectory())
		Expect(filepath.Join(kept.Dir, "attempt-1", "log.txt")).To(BeARegularFile())
	})

	It("leaves a file among the date dirs alone", func() {
		writeSized(runs, "notes.txt", 1)

		Expect(retention.Execute(s, retention.Victims{}, &out)).To(Succeed())
		Expect(filepath.Join(runs, "notes.txt")).To(BeARegularFile())
	})

	// renamed gives the index of the rename of path in the journal.
	renamed := func(path string) int {
		return slices.IndexFunc(fsys.Journal(), func(op faultfs.Op) bool { return op.Name == "rename" && op.Path == path })
	}

	It("writes the horizon at the newest run_created_at of the evicted runs, after the expired runs and extracted trees go and before any evicted run", func() {
		expired := runWithLog("2026-06-01/1_ci_main", time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC))
		extracted := filepath.Join(runs, "2026-10-02", "3_ci_main", "artifacts", "5_report", "extracted")
		writeSized(extracted, "report.xml", 10)
		older := runWithLog("2026-10-01/9_ci_main", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
		newer := runWithLog("2026-10-01/10_ci_main", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC))

		v := retention.Victims{Expired: []string{expired.Dir}, Extracted: []string{extracted}, Evicted: []retention.Run{newer, older}}
		Expect(retention.Execute(s, v, &out)).To(Succeed())
		h, _, err := retention.ReadHorizon(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(h.At).To(BeTemporally("==", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)))
		horizon := renamed(filepath.Join(s.State(), "horizon.json.tmp"))
		Expect(renamed(expired.Dir)).To(BeNumerically("<", renamed(extracted)))
		Expect(renamed(extracted)).To(BeNumerically("<", horizon))
		Expect(horizon).To(BeNumerically("<", renamed(newer.Dir)))
	})

	DescribeTable("on a full disk, evicts runs for disk_cap until it can write the horizon",
		func(full syscall.Errno) {
			expired := runWithLog("2026-06-01/1_ci_main", time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC))
			first := runWithLog("2026-10-01/9_ci_main", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
			second := runWithLog("2026-10-01/10_ci_main", time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC))
			fsys.FailOnUnder("create", filepath.Join(s.State(), "horizon.json.tmp"), full)

			err := retention.Execute(s, retention.Victims{Expired: []string{expired.Dir}, Evicted: []retention.Run{first, second}}, &out)
			Expect(err).To(MatchError(full))
			for _, dir := range []string{expired.Dir, first.Dir, second.Dir} {
				Expect(dir).NotTo(BeADirectory())
			}
			Expect(out.String()).To(Equal(expired.Dir + "\n" + first.Dir + "\n" + second.Dir + "\n"))
		},
		Entry("ENOSPC", syscall.ENOSPC),
		Entry("EDQUOT", syscall.EDQUOT),
	)

	It("keeps a newer horizon", func() {
		newer := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
		Expect(retention.Horizon{At: newer}.Write(s)).To(Succeed())
		evicted := runWithLog("2026-10-01/9_ci_main", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

		Expect(retention.Execute(s, retention.Victims{Evicted: []retention.Run{evicted}}, &out)).To(Succeed())
		h, _, err := retention.ReadHorizon(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(h.At).To(BeTemporally("==", newer))
	})

	It("does not raise the horizon for an evicted run without created_at", func() {
		evicted := runWithLog("2026-10-01/9_ci_main", time.Time{})

		Expect(retention.Execute(s, retention.Victims{Evicted: []retention.Run{evicted}}, &out)).To(Succeed())
		Expect(evicted.Dir).NotTo(BeADirectory())
		Expect(filepath.Join(s.State(), "horizon.json")).NotTo(BeAnExistingFile())
	})

	It("moves a corrupt horizon aside, writes the raised one, evicts everything, and then reports it", func() {
		path := filepath.Join(s.State(), "horizon.json")
		Expect(os.WriteFile(path, []byte("{"), 0o644)).To(Succeed())
		expired := runWithLog("2026-06-01/1_ci_main", time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC))
		evicted := runWithLog("2026-10-01/9_ci_main", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

		err := retention.Execute(s, retention.Victims{Expired: []string{expired.Dir}, Evicted: []retention.Run{evicted}}, &out)
		Expect(err).To(MatchError(ContainSubstring(path)))
		Expect(expired.Dir).NotTo(BeADirectory())
		Expect(evicted.Dir).NotTo(BeADirectory())
		Expect(os.ReadFile(path + ".corrupt")).To(Equal([]byte("{")))
		h, _, err := retention.ReadHorizon(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(h.At).To(BeTemporally("==", evicted.CreatedAt))
	})

	It("writes no horizon when it evicts nothing for disk_cap", func() {
		expired := runWithLog("2026-06-01/1_ci_main", time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC))

		Expect(retention.Execute(s, retention.Victims{Expired: []string{expired.Dir}}, &out)).To(Succeed())
		Expect(filepath.Join(s.State(), "horizon.json")).NotTo(BeAnExistingFile())
	})
})
