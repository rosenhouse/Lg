package daemon_test

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/daemon"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
)

var _ = Describe("Request", Label("daemon"), func() {
	var state string

	BeforeEach(func() {
		state = GinkgoT().TempDir()
	})

	It("counts up from 1 in state/sync-request", func() {
		Expect(daemon.Requested(state)).To(Equal(int64(0)))

		Expect(daemon.Request(store.OSFS{}, state, time.Minute, clock.Real{})).To(Equal(int64(1)))
		Expect(daemon.Request(store.OSFS{}, state, time.Minute, clock.Real{})).To(Equal(int64(2)))

		Expect(os.ReadFile(filepath.Join(state, "sync-request"))).To(Equal([]byte("2\n")))
		Expect(daemon.Requested(state)).To(Equal(int64(2)))
	})

	It("gives distinct, increasing numbers to concurrent requests under state/request.lock", func() {
		const n = 20
		got := make(chan int64, n)
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				defer GinkgoRecover()
				number, err := daemon.Request(store.OSFS{}, state, time.Minute, clock.Real{})
				Expect(err).NotTo(HaveOccurred())
				got <- number
			})
		}
		wg.Wait()
		close(got)

		var numbers []int64
		for number := range got {
			numbers = append(numbers, number)
		}
		want := make([]int64, n)
		for i := range want {
			want[i] = int64(i + 1)
		}
		Expect(numbers).To(ConsistOf(want))
		Expect(daemon.Requested(state)).To(Equal(int64(n)))
	})

	DescribeTable("waits for a busy state/request.lock up to wait, and 10s at most",
		func(wait, want time.Duration) {
			held, err := lock.Wait(filepath.Join(state, "request.lock"), time.Second, clock.Real{}, func(string) {})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(held.Release)
			clk := clock.NewFake(t0)
			done := make(chan error, 1)
			go func() {
				_, err := daemon.Request(store.OSFS{}, state, wait, clk)
				done <- err
			}()
			Eventually(clk.Waiting, time.Second).Should(Equal(2))

			clk.Set(t0.Add(want))

			Eventually(done, time.Second).Should(Receive(MatchError(HaveSuffix("gave up after %s", want))))
		},
		Entry("under 10s", time.Second, time.Second),
		Entry("over 10s", time.Hour, 10*time.Second),
	)

	It("writes state/sync-request through its FS", func() {
		fsys := faultfs.New()
		fsys.FailOnUnder("create", state, syscall.ENOSPC)

		_, err := daemon.Request(fsys, state, time.Minute, clock.Real{})
		Expect(err).To(MatchError(syscall.ENOSPC))
	})

	DescribeTable("names state/sync-request in Requested when it does not hold a request number",
		func(content string) {
			path := filepath.Join(state, "sync-request")
			Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())

			_, err := daemon.Requested(state)
			Expect(err).To(MatchError(ContainSubstring(path)))
		},
		Entry("no number", "many\n"),
		Entry("a negative number", "-3\n"),
		Entry("the largest number, which has no next", "9223372036854775807\n"),
	)

	DescribeTable("numbers a request after status.json's served_request, whatever state/sync-request holds",
		func(content string) {
			Expect(os.WriteFile(filepath.Join(state, "status.json"), []byte(`{"served_request": 7}`), 0o644)).To(Succeed())
			if content != "" {
				Expect(os.WriteFile(filepath.Join(state, "sync-request"), []byte(content), 0o644)).To(Succeed())
			}

			Expect(daemon.Request(store.OSFS{}, state, time.Minute, clock.Real{})).To(Equal(int64(8)))
			Expect(daemon.Requested(state)).To(Equal(int64(8)))
		},
		Entry("nothing, after a reset", ""),
		Entry("a lower number", "2\n"),
		Entry("no number", "many\n"),
		Entry("the largest number", "9223372036854775807\n"),
	)

	It("numbers a request 1 when status.json's served_request is the largest number", func() {
		Expect(os.WriteFile(filepath.Join(state, "status.json"), []byte(`{"served_request": 9223372036854775807}`), 0o644)).To(Succeed())

		Expect(daemon.Request(store.OSFS{}, state, time.Minute, clock.Real{})).To(Equal(int64(1)))
	})

	It("numbers a request after a higher state/sync-request than status.json's served_request", func() {
		Expect(os.WriteFile(filepath.Join(state, "status.json"), []byte(`{"served_request": 7}`), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(state, "sync-request"), []byte("9\n"), 0o644)).To(Succeed())

		Expect(daemon.Request(store.OSFS{}, state, time.Minute, clock.Real{})).To(Equal(int64(10)))
	})
})
