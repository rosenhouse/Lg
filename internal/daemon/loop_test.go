package daemon_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/daemon"
)

// started is a cycle as the loop called it.
type started struct {
	At     time.Time
	Served int64
}

// loopEnv runs a daemon.Loop on a fake clock whose cycles return outcomes
// in turn, the last one again once they run out.
type loopEnv struct {
	clk       *clock.Fake
	requested atomic.Int64
	outcomes  []daemon.Outcome
	cycles    chan started
	// during runs inside each cycle.
	during func(ctx context.Context)
	log    syncBuffer
	loop   daemon.Loop
}

func newLoopEnv(outcomes ...daemon.Outcome) *loopEnv {
	e := &loopEnv{clk: clock.NewFake(t0), outcomes: outcomes, cycles: make(chan started, 100), during: func(context.Context) {}}
	e.loop = daemon.Loop{
		Clock: e.clk,
		Cycle: func(ctx context.Context, served int64) daemon.Outcome {
			e.cycles <- started{At: e.clk.Now(), Served: served}
			e.during(ctx)
			out := e.outcomes[0]
			if len(e.outcomes) > 1 {
				e.outcomes = e.outcomes[1:]
			}
			return out
		},
		Requested: func() (int64, error) { return e.requested.Load(), nil },
		Reconcile: func(context.Context) error { return nil },
		Log:       &e.log,
	}
	return e
}

// run runs the loop until the spec ends, and closes the returned channel when it returns.
func (e *loopEnv) run() (cancel func(), returned <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		e.loop.Run(ctx)
	}()
	DeferCleanup(func() {
		cancel()
		Eventually(done, time.Second).Should(BeClosed())
	})
	return cancel, done
}

func (e *loopEnv) expectCycle(at time.Time, served int64) {
	GinkgoHelper()
	Eventually(e.cycles, time.Second).Should(Receive(Equal(started{At: at, Served: served})))
}

func (e *loopEnv) expectNoCycle() {
	GinkgoHelper()
	Consistently(e.cycles, 100*time.Millisecond).ShouldNot(Receive())
}

// every is an outcome of a cycle that started at at, with interval 10m.
func every(at time.Time) daemon.Outcome {
	return daemon.Outcome{Started: at, Interval: 10 * time.Minute}
}

var _ = Describe("Loop", Label("daemon"), func() {
	It("runs the first cycle at once, and the next at max(start+interval, retry_at)", func() {
		blocked := every(t0.Add(10 * time.Minute))
		blocked.RetryAt = t0.Add(30 * time.Minute)
		e := newLoopEnv(every(t0), blocked, every(t0.Add(30*time.Minute)))
		e.run()

		e.expectCycle(t0, 0)
		e.clk.Set(t0.Add(10*time.Minute - time.Second))
		e.expectNoCycle()
		e.clk.Set(t0.Add(10 * time.Minute))
		e.expectCycle(t0.Add(10*time.Minute), 0)
		e.clk.Set(t0.Add(20 * time.Minute))
		e.expectNoCycle()
		e.clk.Set(t0.Add(30 * time.Minute))
		e.expectCycle(t0.Add(30*time.Minute), 0)
	})

	It("starts a cycle at once for a request during the wait, which serves it", func() {
		e := newLoopEnv(every(t0))
		e.run()
		e.expectCycle(t0, 0)

		e.requested.Store(1)
		e.clk.Set(t0.Add(time.Second))

		e.expectCycle(t0.Add(time.Second), 1)
	})

	It("serves a request during the wait by the cycle at a future retry_at, not at once", func() {
		blocked := every(t0)
		blocked.RetryAt = t0.Add(5 * time.Minute)
		e := newLoopEnv(blocked, every(t0.Add(5*time.Minute)))
		e.run()
		e.expectCycle(t0, 0)

		e.requested.Store(1)
		e.clk.Set(t0.Add(time.Second))
		e.expectNoCycle()
		e.clk.Set(t0.Add(5 * time.Minute))

		e.expectCycle(t0.Add(5*time.Minute), 1)
	})

	It("coalesces requests during a cycle into one follow-up, which serves them all", func() {
		e := newLoopEnv(every(t0))
		e.during = func(context.Context) {
			if e.requested.Load() == 0 {
				for range 3 {
					e.requested.Add(1)
				}
			}
		}
		e.run()

		e.expectCycle(t0, 0)
		e.expectCycle(t0, 3)
		e.clk.Set(t0.Add(time.Second))
		e.expectNoCycle()
	})

	It("returns after the current cycle when ctx is cancelled", func() {
		e := newLoopEnv(every(t0))
		proceed := make(chan struct{})
		e.during = func(context.Context) { <-proceed }
		cancel, returned := e.run()
		e.expectCycle(t0, 0)

		cancel()
		Consistently(returned, 100*time.Millisecond).ShouldNot(BeClosed())
		close(proceed)

		Eventually(returned, time.Second).Should(BeClosed())
		Expect(e.cycles).NotTo(Receive())
	})

	It("returns when ctx is cancelled during the wait", func() {
		e := newLoopEnv(every(t0))
		cancel, returned := e.run()
		e.expectCycle(t0, 0)

		cancel()

		Eventually(returned, time.Second).Should(BeClosed())
	})

	It("reconciles the index after each cycle, and logs a reconcile error without stopping", func() {
		e := newLoopEnv(every(t0), every(t0.Add(10*time.Minute)))
		var mu sync.Mutex
		var events []string
		record := func(event string) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		}
		e.during = func(context.Context) { record("cycle") }
		e.loop.Reconcile = func(context.Context) error {
			record("reconcile")
			return errors.New("database is locked")
		}
		e.run()
		e.expectCycle(t0, 0)
		e.clk.Set(t0.Add(10 * time.Minute))
		e.expectCycle(t0.Add(10*time.Minute), 0)

		Eventually(func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), events...)
		}, time.Second).Should(Equal([]string{"cycle", "reconcile", "cycle", "reconcile"}))
		Expect(e.log.String()).To(ContainSubstring("database is locked"))
	})

	It("logs one line per cycle with its start, its error and when the next is due", func() {
		failed := every(t0)
		failed.Err = errors.New("GET /repos/rosenhouse/lg:\n502 Bad Gateway")
		e := newLoopEnv(failed)
		e.run()
		e.expectCycle(t0, 0)

		Eventually(e.log.String, time.Second).Should(Equal(
			"lg: sync at 2026-10-03T18:00:00Z: GET /repos/rosenhouse/lg: 502 Bad Gateway; next sync at 2026-10-03T18:10:00Z\n"))
	})

	It("truncates a regular file over 10 MB that it logs to at cycle start", func() {
		e := newLoopEnv(every(t0))
		log, err := os.Create(filepath.Join(GinkgoT().TempDir(), "daemon.log"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(log.Close)
		Expect(log.Truncate(10_000_001)).To(Succeed())
		_, err = log.Seek(0, 2)
		Expect(err).NotTo(HaveOccurred())
		e.loop.Log = log
		e.run()
		e.expectCycle(t0, 0)

		Eventually(func() ([]byte, error) { return os.ReadFile(log.Name()) }, time.Second).
			Should(Equal([]byte("lg: sync at 2026-10-03T18:00:00Z: ok; next sync at 2026-10-03T18:10:00Z\n")))
	})

	It("keeps a regular file of 10 MB that it logs to", func() {
		e := newLoopEnv(every(t0))
		log, err := os.Create(filepath.Join(GinkgoT().TempDir(), "daemon.log"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(log.Close)
		Expect(log.Truncate(10_000_000)).To(Succeed())
		_, err = log.Seek(0, 2)
		Expect(err).NotTo(HaveOccurred())
		e.loop.Log = log
		e.run()
		e.expectCycle(t0, 0)

		Eventually(func() (int64, error) {
			info, err := os.Stat(log.Name())
			return info.Size(), err
		}, time.Second).Should(BeNumerically(">", 10_000_000))
	})
})

// syncBuffer is a bytes.Buffer that the loop and a spec may use at once.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
