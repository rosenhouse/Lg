package retention_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/retention"
)

var _ = Describe("retention.Find over 20,000 runs", Label("retention", "scale"), func() {
	It("reads their 49 KB fetch.json files, as a 200-job matrix writes, within 5s", func() {
		root := GinkgoT().TempDir()
		var sources []string
		for i := range 200 {
			sources = append(sources, fmt.Sprintf(`"jobs/%d_build-ubuntu-1.22-shard-%d/log.txt":{"url":"https://api.github.com/repos/o/r/actions/jobs/%d/logs","status":200,"bytes":123456,"sha256":"%s"}`, i, i, i, strings.Repeat("0", 64)))
		}
		fetch := filepath.Join(root, "fetch.json")
		content := `{"lg_format":1,"run_id":1,"run_created_at":"2026-08-01T01:00:00Z","sources":{` + strings.Join(sources, ",") + `}}`
		Expect(os.WriteFile(fetch, []byte(content), 0o644)).To(Succeed())
		data := filepath.Join(root, "data")
		for i := range 20_000 {
			date := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i/250).Format(time.DateOnly)
			attempt := filepath.Join(data, "github.com/o/r/runs", date, fmt.Sprintf("%d_ci_main", i), "attempt-1")
			Expect(os.MkdirAll(attempt, 0o755)).To(Succeed())
			// One file, linked, keeps the spec's disk use small.
			Expect(os.Link(fetch, filepath.Join(attempt, "fetch.json"))).To(Succeed())
		}

		start := clock.Real{}.Now()
		_, err := retention.Find(data, nil, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), 90*24*time.Hour, 50<<30)
		Expect(err).NotTo(HaveOccurred())
		Expect(clock.Real{}.Now().Sub(start)).To(BeNumerically("<", 5*time.Second))
	})
})
