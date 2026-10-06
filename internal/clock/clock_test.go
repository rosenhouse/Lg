package clock_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
)

var t0 = time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)

var _ = Describe("Fake", Label("failures"), func() {
	It("reads the time it was last Set to", func() {
		fake := clock.NewFake(t0)
		Expect(fake.Now()).To(Equal(t0))

		fake.Set(t0.Add(time.Hour))
		Expect(fake.Now()).To(Equal(t0.Add(time.Hour)))
	})

	It("fires After once Set reaches its deadline", func() {
		fake := clock.NewFake(t0)
		fired := fake.After(time.Minute)

		fake.Set(t0.Add(time.Minute - time.Nanosecond))
		Consistently(fired, 50*time.Millisecond).ShouldNot(Receive())
		fake.Set(t0.Add(time.Minute))
		Eventually(fired, time.Second).Should(Receive(Equal(t0.Add(time.Minute))))
	})

	It("counts the Afters still waiting", func() {
		fake := clock.NewFake(t0)
		fake.After(time.Minute)
		fake.After(time.Hour)
		fake.After(0)
		Expect(fake.Waiting()).To(Equal(2))

		fake.Set(t0.Add(time.Minute))
		Expect(fake.Waiting()).To(Equal(1))
	})

	DescribeTable("fires After at once for a duration that is not positive",
		func(d time.Duration) {
			Expect(clock.NewFake(t0).After(d)).To(Receive(Equal(t0)))
		},
		Entry("zero", time.Duration(0)),
		Entry("negative", -time.Second),
	)
})

var _ = Describe("Real", Label("failures"), func() {
	It("reads the system time", func() {
		Expect(clock.Real{}.Now()).To(BeTemporally("~", time.Now(), time.Second))
	})
})

var _ = Describe("FromEnv", Label("failures"), func() {
	It("starts at LG_TEST_NOW and advances as the base clock does", func() {
		base := clock.NewFake(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
		clk, err := clock.FromEnv(map[string]string{"LG_TEST_NOW": "2026-10-03T18:00:00Z"}, base)
		Expect(err).NotTo(HaveOccurred())
		Expect(clk.Now()).To(Equal(t0))

		base.Set(base.Now().Add(90 * time.Second))
		Expect(clk.Now()).To(Equal(t0.Add(90 * time.Second)))
	})

	It("starts at an LG_TEST_NOW centuries from the base clock", func() {
		base := clock.NewFake(t0)
		clk, err := clock.FromEnv(map[string]string{"LG_TEST_NOW": "1700-01-01T00:00:00Z"}, base)
		Expect(err).NotTo(HaveOccurred())
		Expect(clk.Now()).To(Equal(time.Date(1700, 1, 1, 0, 0, 0, 0, time.UTC)))
	})

	It("advances in real time over Real", func() {
		clk, err := clock.FromEnv(map[string]string{"LG_TEST_NOW": "2026-10-03T18:00:00Z"}, clock.Real{})
		Expect(err).NotTo(HaveOccurred())
		time.Sleep(20 * time.Millisecond)
		Expect(clk.Now()).To(BeTemporally(">=", t0.Add(20*time.Millisecond)))
		Expect(clk.Now()).To(BeTemporally("<", t0.Add(time.Minute)))
	})

	It("waits on the base clock", func() {
		base := clock.NewFake(t0)
		clk, err := clock.FromEnv(map[string]string{"LG_TEST_NOW": "2020-01-01T00:00:00Z"}, base)
		Expect(err).NotTo(HaveOccurred())
		fired := clk.After(time.Minute)

		base.Set(t0.Add(time.Minute))
		Eventually(fired, time.Second).Should(Receive())
	})

	It("is the base clock when LG_TEST_NOW is unset or empty", func() {
		base := clock.NewFake(t0)
		for _, env := range []map[string]string{{}, {"LG_TEST_NOW": ""}} {
			clk, err := clock.FromEnv(env, base)
			Expect(err).NotTo(HaveOccurred())
			Expect(clk).To(BeIdenticalTo(base))
		}
	})

	It("fails on an LG_TEST_NOW that is not RFC 3339", func() {
		_, err := clock.FromEnv(map[string]string{"LG_TEST_NOW": "2026-10-03 18:00"}, clock.Real{})
		Expect(err).To(MatchError(ContainSubstring(`LG_TEST_NOW: parsing time "2026-10-03 18:00"`)))
	})
})
