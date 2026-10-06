package daemon_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/daemon"
)

var t0 = time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)

var _ = Describe("Outcome.Next", Label("daemon"), func() {
	DescribeTable("is start plus interval, or retry_at when later",
		func(retryAt, next time.Time) {
			Expect(daemon.Outcome{Started: t0, Interval: 10 * time.Minute, RetryAt: retryAt}.Next()).To(Equal(next))
		},
		Entry("not blocked", time.Time{}, t0.Add(10*time.Minute)),
		Entry("retry_at before the interval ends", t0.Add(time.Minute), t0.Add(10*time.Minute)),
		Entry("retry_at after the interval ends", t0.Add(30*time.Minute), t0.Add(30*time.Minute)),
	)

	It("counts the interval from the start's second, as status.json records it", func() {
		Expect(daemon.Outcome{Started: t0.Add(999 * time.Millisecond), Interval: 10 * time.Minute}.Next()).To(Equal(t0.Add(10 * time.Minute)))
	})

	DescribeTable("carries no monotonic reading, so a wait compares wall clocks across a suspend",
		func(retryAfter time.Duration) {
			now := clock.Real{}.Now()
			Expect(daemon.Outcome{Started: now, Interval: 10 * time.Minute, RetryAt: now.Add(retryAfter)}.Next().String()).NotTo(ContainSubstring("m="))
		},
		Entry("not blocked", time.Duration(0)),
		Entry("blocked past the interval", time.Hour),
	)
})
