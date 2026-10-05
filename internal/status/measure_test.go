package status_test

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/status"
)

// writeFile writes content to root/rel, making its dirs.
func writeFile(root, rel, content string) {
	GinkgoHelper()
	path := filepath.Join(root, rel)
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}

func fetchJSON(createdAt time.Time) string {
	return fmt.Sprintf(`{"lg_format":1,"run_created_at":%q}`, createdAt.Format(time.RFC3339))
}

var _ = Describe("Measure", Label("status"), func() {
	It("measures the repo's runs on disk, matching its name in any case, with the newest completed run and the horizon", MustPassRepeatedly(20), func() {
		root := GinkgoT().TempDir()
		data, state := filepath.Join(root, "data"), filepath.Join(root, "state")
		completed := time.Date(2026, 10, 3, 14, 22, 54, 0, time.UTC)
		older := "github.com/rosenhouse/Lg/runs/2026-10-02/1_ci_main"
		writeFile(data, older+"/attempt-1/fetch.json", fetchJSON(completed.Add(-24*time.Hour)))
		run := "github.com/rosenhouse/Lg/runs/2026-10-03/2_ci_main"
		writeFile(data, run+"/attempt-1/fetch.json", fetchJSON(completed))
		writeFile(data, run+"/attempt-2/log.txt", "0123456789")
		artifactsOnly := "github.com/rosenhouse/Lg/runs/2026-10-03/3_ci_main"
		writeFile(data, artifactsOnly+"/artifacts/9_report/fetch.json", fetchJSON(completed.Add(time.Hour)))
		writeFile(data, "github.com/rosenhouse/other/runs/2026-10-03/4_ci_main/attempt-1/fetch.json", fetchJSON(completed.Add(2*time.Hour)))
		writeFile(state, "horizon.json", `{"github.com/rosenhouse/Lg":"2026-09-01T00:00:00Z","github.com/rosenhouse/lg":"2026-08-01T00:00:00Z","github.com/rosenhouse/other":"2026-09-30T00:00:00Z"}`)

		disk, err := status.Measure(data, state, "github.com/rosenhouse/lg")

		Expect(err).NotTo(HaveOccurred())
		Expect(disk).To(Equal(status.Disk{
			Runs:            3,
			Attempts:        3,
			Bytes:           int64(3*len(fetchJSON(completed)) + 10),
			NewestCompleted: completed,
			Horizon:         time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		}))
	})

	It("measures nothing before the first sync", func() {
		root := GinkgoT().TempDir()

		Expect(status.Measure(filepath.Join(root, "data"), filepath.Join(root, "state"), "github.com/o/r")).To(BeZero())
	})
})
