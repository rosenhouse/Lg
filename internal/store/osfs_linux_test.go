package store_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/store"
)

var _ = Describe("OSFS.Mount on Linux", Label("store"), func() {
	It("reports mount ids, which tell mounts apart", func() {
		tmp, err := store.OSFS{}.Mount(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		proc, err := store.OSFS{}.Mount("/proc")
		Expect(err).NotTo(HaveOccurred())

		Expect(tmp.ID).NotTo(BeZero())
		Expect(proc.ID).NotTo(Equal(tmp.ID))
	})
})
