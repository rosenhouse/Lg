package store_test

import (
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/sys/unix"

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

	DescribeTable("falls back to stat's device where statx is unavailable",
		func(errno syscall.Errno) {
			dir := GinkgoT().TempDir()
			var st unix.Stat_t
			Expect(unix.Stat(dir, &st)).To(Succeed())

			Expect(store.MountWith(failingStatx(errno), dir)).To(Equal(store.Mount{Dev: st.Dev}))
		},
		Entry("old kernel", syscall.ENOSYS),
		Entry("seccomp", syscall.EPERM),
	)

	It("reports any other statx error", func() {
		Expect(store.MountWith(failingStatx(syscall.EIO), GinkgoT().TempDir())).Error().To(MatchError(syscall.EIO))
	})
})

func failingStatx(errno syscall.Errno) func(int, string, int, int, *unix.Statx_t) error {
	return func(int, string, int, int, *unix.Statx_t) error { return errno }
}
