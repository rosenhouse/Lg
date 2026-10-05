package lock_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
)

var ignore = func(string) {}

// giveUp bounds how long a spec waits for Wait to time out, so a Wait that
// ignores its timeout fails the spec instead of hanging it.
const giveUp = 2 * time.Second

var _ = Describe("Wait", Label("store"), func() {
	var path string

	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "write.lock")
	})

	// holdAs takes the lock and then writes content over the pid it recorded.
	holdAs := func(content string) {
		GinkgoHelper()
		held, err := lock.Wait(path, time.Second, clock.Real{}, ignore)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
	}

	// waitAsync calls Wait in a goroutine and sends its error.
	waitAsync := func(timeout time.Duration, clk clock.Clock, waiting func(string)) <-chan error {
		got := make(chan error, 1)
		go func() {
			l, err := lock.Wait(path, timeout, clk, waiting)
			if err == nil {
				err = l.Release()
			}
			got <- err
		}()
		return got
	}

	It("takes a free lock and records the holder's pid over a stale longer one", func() {
		Expect(os.WriteFile(path, []byte("9999999999\n"), 0o644)).To(Succeed())

		held, err := lock.Wait(path, time.Second, clock.Real{}, ignore)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)

		Expect(os.ReadFile(path)).To(Equal(fmt.Appendf(nil, "%d\n", os.Getpid())))
	})

	It("clears the pid on Release, so no waiter names a holder that has exited", func() {
		held, err := lock.Wait(path, time.Second, clock.Real{}, ignore)
		Expect(err).NotTo(HaveOccurred())

		Expect(held.Release()).To(Succeed())
		Expect(os.ReadFile(path)).To(BeEmpty())
	})

	It("gives up after its timeout with ErrTimeout naming the holder's pid", func() {
		holdAs("424242\n")

		Eventually(waitAsync(50*time.Millisecond, clock.Real{}, ignore), giveUp).Should(Receive(And(
			MatchError(lock.ErrTimeout),
			MatchError(path+" is held by pid 424242; gave up after 50ms"))))
	})

	It("arms its deadline with the timeout it is given", func() {
		holdAs("424242\n")
		clk := &recordingClock{}

		Eventually(waitAsync(70*time.Millisecond, clk, ignore), giveUp).Should(Receive(MatchError(lock.ErrTimeout)))
		Expect(clk.requested()).To(ContainElement(70 * time.Millisecond))
	})

	DescribeTable("names an unknown holder when the lock file holds no pid",
		func(content string) {
			holdAs(content)

			Eventually(waitAsync(50*time.Millisecond, clock.Real{}, ignore), giveUp).Should(Receive(
				MatchError(path + " is held by another process (pid unknown); gave up after 50ms")))
		},
		Entry("empty", ""),
		Entry("not a number", "flock\n"),
	)

	It("tells waiting, once, who holds a busy lock", func() {
		holdAs("424242\n")

		var told []string
		Eventually(waitAsync(200*time.Millisecond, clock.Real{}, func(holder string) { told = append(told, holder) }), giveUp).
			Should(Receive(HaveOccurred()))
		Expect(told).To(Equal([]string{"pid 424242"}))
	})

	It("takes the lock once its holder releases it", func() {
		held, err := lock.Wait(path, time.Second, clock.Real{}, ignore)
		Expect(err).NotTo(HaveOccurred())
		got := waitAsync(5*time.Second, clock.Real{}, ignore)

		Consistently(got, 200*time.Millisecond).ShouldNot(Receive())
		Expect(held.Release()).To(Succeed())
		Eventually(got, giveUp).Should(Receive(BeNil()))
	})
})

// recordingClock is the real clock, recording each duration it is asked for.
type recordingClock struct {
	clock.Real
	mu        sync.Mutex
	durations []time.Duration
}

func (c *recordingClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.durations = append(c.durations, d)
	return clock.Real{}.After(d)
}

func (c *recordingClock) requested() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.durations...)
}

var _ = Describe("Held", Label("status"), func() {
	var path string

	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "daemon.lock")
	})

	It("reports false, creating nothing, when no file is there", func() {
		Expect(lock.Held(path)).To(BeFalse())
		Expect(path).NotTo(BeAnExistingFile())
	})

	It("reports true while a holder holds the lock, and false after it releases", func() {
		held, err := lock.Wait(path, time.Second, clock.Real{}, ignore)
		Expect(err).NotTo(HaveOccurred())

		Expect(lock.Held(path)).To(BeTrue())
		Expect(held.Release()).To(Succeed())
		Expect(lock.Held(path)).To(BeFalse())
	})

	It("delays a Wait with no timeout that overlaps it, without failing the Wait", func() {
		Expect(os.WriteFile(path, nil, 0o644)).To(Succeed())
		shared, err := os.Open(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = shared.Close() })
		Expect(syscall.Flock(int(shared.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)).To(Succeed(), "as Held does")

		held, err := lock.Wait(path, 0, clock.Real{}, func(string) { Expect(shared.Close()).To(Succeed()) })

		Expect(err).NotTo(HaveOccurred())
		Expect(held.Release()).To(Succeed())
	})

	It("leaves the lock free for Wait", func() {
		Expect(os.WriteFile(path, nil, 0o644)).To(Succeed())
		Expect(lock.Held(path)).To(BeFalse())

		held, err := lock.Wait(path, 0, clock.Real{}, ignore)
		Expect(err).NotTo(HaveOccurred())
		Expect(held.Release()).To(Succeed())
	})
})
