package lock_test

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
)

var _ = Describe("Wait", Label("store"), func() {
	var path string

	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "write.lock")
	})

	It("takes a free lock and records the holder's pid over a stale longer one", func() {
		Expect(os.WriteFile(path, []byte("9999999999\n"), 0o644)).To(Succeed())

		held, err := lock.Wait(path, time.Second, clock.Real{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)

		Expect(os.ReadFile(path)).To(Equal(fmt.Appendf(nil, "%d\n", os.Getpid())))
	})

	It("gives up after its timeout with an error naming the holder's pid", func() {
		held, err := lock.Wait(path, time.Second, clock.Real{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		Expect(os.WriteFile(path, []byte("424242\n"), 0o644)).To(Succeed())

		_, err = lock.Wait(path, 50*time.Millisecond, clock.Real{})
		Expect(err).To(MatchError(path + " is held by pid 424242; gave up after 50ms"))
	})

	It("takes the lock once its holder releases it", func() {
		held, err := lock.Wait(path, time.Second, clock.Real{})
		Expect(err).NotTo(HaveOccurred())
		got := make(chan error)
		go func() {
			l, err := lock.Wait(path, 5*time.Second, clock.Real{})
			if err == nil {
				err = l.Release()
			}
			got <- err
		}()

		Consistently(got, 200*time.Millisecond).ShouldNot(Receive())
		Expect(held.Release()).To(Succeed())
		Eventually(got, 2*time.Second).Should(Receive(BeNil()))
	})
})
