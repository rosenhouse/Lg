package retention_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/retention"
)

// writeFetch writes dir/fetch.json with run_created_at createdAt.
func writeFetch(dir string, createdAt time.Time) {
	GinkgoHelper()
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	fetch := fmt.Sprintf(`{"lg_format":1,"run_created_at":%q}`, createdAt.Format(time.RFC3339))
	Expect(os.WriteFile(filepath.Join(dir, "fetch.json"), []byte(fetch), 0o644)).To(Succeed())
}

// writeSized writes size bytes to root/rel, making its dirs.
func writeSized(root, rel string, size int) {
	GinkgoHelper()
	path := filepath.Join(root, rel)
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644)).To(Succeed())
}

var _ = Describe("retention.Scan", Label("retention"), func() {
	It("counts the apparent bytes of regular files under data/ only, per run and per extracted/ tree", func() {
		root := GinkgoT().TempDir()
		data := filepath.Join(root, "data")
		run := filepath.Join(data, "github.com/o/r/runs/2026-10-01/12_ci_main")
		writeSized(run, "attempt-1/log.txt", 100)
		writeSized(run, "artifacts/5_report/artifact.zip", 10)
		writeSized(run, "artifacts/5_report/extracted/a/b.txt", 7)
		writeSized(run, "artifacts/6_cov/extracted/c.txt", 3)
		Expect(os.Symlink("/etc/passwd", filepath.Join(run, "attempt-1", "link"))).To(Succeed())
		other := filepath.Join(data, "ghe.example.com/a/b/runs/2026-09-01/3_x_y")
		writeSized(other, "attempt-1/log.txt", 20)
		empty := filepath.Join(data, "ghe.example.com/a/b/runs/2026-09-02/4_x_y")
		Expect(os.MkdirAll(empty, 0o755)).To(Succeed())
		writeSized(data, "github.com/o/r/notes.txt", 5)
		writeSized(root, "state/lg.db", 1000)
		writeSized(root, "tmp/unit-x/log.txt", 1000)

		u, err := retention.Scan(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(u.Bytes).To(Equal(int64(145)))
		Expect(u.Runs).To(ConsistOf(
			retention.Run{Dir: run, Date: "2026-10-01", ID: 12, Bytes: 120, Extracted: []retention.Tree{
				{Dir: filepath.Join(run, "artifacts/5_report/extracted"), Bytes: 7},
				{Dir: filepath.Join(run, "artifacts/6_cov/extracted"), Bytes: 3},
			}},
			retention.Run{Dir: other, Date: "2026-09-01", ID: 3, Bytes: 20},
			retention.Run{Dir: empty, Date: "2026-09-02", ID: 4},
		))
	})

	It("takes each run's created_at from a fetch.json of an attempt or else of an artifact", func() {
		data := filepath.Join(GinkgoT().TempDir(), "data")
		runs := filepath.Join(data, "github.com/o/r/runs/2026-10-01")
		writeFetch(filepath.Join(runs, "1_ci_main", "attempt-2"), time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC))
		writeFetch(filepath.Join(runs, "2_ci_main", "artifacts", "5_report"), time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC))
		Expect(os.MkdirAll(filepath.Join(runs, "3_ci_main", "artifacts"), 0o755)).To(Succeed())

		u, err := retention.Scan(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(u.Runs).To(HaveExactElements(
			HaveField("CreatedAt", time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)),
			HaveField("CreatedAt", time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)),
			HaveField("CreatedAt", BeZero()),
		))
	})

	It("finds nothing in a missing data/", func() {
		u, err := retention.Scan(filepath.Join(GinkgoT().TempDir(), "data"))
		Expect(err).NotTo(HaveOccurred())
		Expect(u).To(BeZero())
	})
})
