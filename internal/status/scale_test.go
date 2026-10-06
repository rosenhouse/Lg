package status_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/status"
)

var _ = Describe("Next with 50,000 units pending", Label("scale"), func() {
	It("runs in under 1s", func() {
		started := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
		var pending []status.Pending
		for i := range 50_000 {
			pending = append(pending, status.Pending{Unit: status.Unit{Run: int64(i), Attempt: 1}, Error: "503"})
		}
		first := good(started)
		first.Pending = pending
		prev := status.Next(nil, first)
		c := good(started.Add(10 * time.Minute))
		c.Pending = pending

		start := clock.Real{}.Now()
		st := status.Next(&prev, c)
		took := clock.Real{}.Now().Sub(start)
		AddReportEntry("timings", fmt.Sprintf("Next %s", took))

		Expect(took).To(BeNumerically("<", time.Second))
		Expect(st.Repos[repo].PendingUnits).To(Equal(50_000))
	})
})
