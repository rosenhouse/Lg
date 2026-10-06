package daemon_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/daemon"
)

var t0 = time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)

var _ = DescribeTable("Next is start plus interval, or retry_at when later", Label("daemon"),
	func(retryAt, next time.Time) {
		Expect(daemon.Next(t0, 10*time.Minute, retryAt)).To(Equal(next))
	},
	Entry("not blocked", time.Time{}, t0.Add(10*time.Minute)),
	Entry("retry_at before the interval ends", t0.Add(time.Minute), t0.Add(10*time.Minute)),
	Entry("retry_at after the interval ends", t0.Add(30*time.Minute), t0.Add(30*time.Minute)),
)
