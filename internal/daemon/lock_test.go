package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/daemon"
)

var _ = Describe("LockInstance", Label("daemon"), func() {
	var state string

	BeforeEach(func() {
		state = GinkgoT().TempDir()
	})

	It("records the holder's pid in state/daemon.pid", func() {
		held, err := daemon.LockInstance(state, clock.Real{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)

		Expect(os.ReadFile(filepath.Join(state, "daemon.pid"))).To(Equal(fmt.Appendf(nil, "%d\n", os.Getpid())))
	})

	It("refuses a second holder and reports the pid from state/daemon.pid", func() {
		held, err := daemon.LockInstance(state, clock.Real{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		Expect(os.WriteFile(filepath.Join(state, "daemon.pid"), []byte("4242\n"), 0o644)).To(Succeed())

		_, err = daemon.LockInstance(state, clock.Real{})

		Expect(err).To(MatchError("already running (pid 4242)"))
	})

	It("reports an unknown pid when state/daemon.pid is missing", func() {
		held, err := daemon.LockInstance(state, clock.Real{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		Expect(os.Remove(filepath.Join(state, "daemon.pid"))).To(Succeed())

		_, err = daemon.LockInstance(state, clock.Real{})

		Expect(err).To(MatchError("already running (pid unknown)"))
	})

	It("lets the next holder in after Release, which removes state/daemon.pid", func() {
		held, err := daemon.LockInstance(state, clock.Real{})
		Expect(err).NotTo(HaveOccurred())
		Expect(held.Release()).To(Succeed())
		Expect(filepath.Join(state, "daemon.pid")).NotTo(BeAnExistingFile())

		next, err := daemon.LockInstance(state, clock.Real{})
		Expect(err).NotTo(HaveOccurred())
		Expect(next.Release()).To(Succeed())
	})
})

var _ = Describe("Request", Label("daemon"), func() {
	var state string

	BeforeEach(func() {
		state = GinkgoT().TempDir()
	})

	It("counts up from 1 in state/sync-request", func() {
		Expect(daemon.Requested(state)).To(Equal(int64(0)))

		Expect(daemon.Request(state, clock.Real{})).To(Equal(int64(1)))
		Expect(daemon.Request(state, clock.Real{})).To(Equal(int64(2)))

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
				number, err := daemon.Request(state, clock.Real{})
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

	It("names state/sync-request in Requested when it does not hold a number", func() {
		path := filepath.Join(state, "sync-request")
		Expect(os.WriteFile(path, []byte("many\n"), 0o644)).To(Succeed())

		_, err := daemon.Requested(state)
		Expect(err).To(MatchError(ContainSubstring(path)))
	})

	DescribeTable("numbers a request after status.json's served_request, whatever state/sync-request holds",
		func(content string) {
			Expect(os.WriteFile(filepath.Join(state, "status.json"), []byte(`{"served_request": 7}`), 0o644)).To(Succeed())
			if content != "" {
				Expect(os.WriteFile(filepath.Join(state, "sync-request"), []byte(content), 0o644)).To(Succeed())
			}

			Expect(daemon.Request(state, clock.Real{})).To(Equal(int64(8)))
			Expect(daemon.Requested(state)).To(Equal(int64(8)))
		},
		Entry("nothing, after a reset", ""),
		Entry("a lower number", "2\n"),
		Entry("no number", "many\n"),
	)

	It("numbers a request after a higher state/sync-request than status.json's served_request", func() {
		Expect(os.WriteFile(filepath.Join(state, "status.json"), []byte(`{"served_request": 7}`), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(state, "sync-request"), []byte("9\n"), 0o644)).To(Succeed())

		Expect(daemon.Request(state, clock.Real{})).To(Equal(int64(10)))
	})
})
