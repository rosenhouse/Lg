package lock_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
)

var _ = Describe("Wait on Linux", Label("store"), func() {
	It("takes a lock whose file cannot hold its pid, as on a full disk", func() {
		held, err := lock.Wait("/dev/full", time.Second, clock.Real{}, ignore)
		Expect(err).NotTo(HaveOccurred())
		Expect(held.Release()).To(Succeed())
	})
})
