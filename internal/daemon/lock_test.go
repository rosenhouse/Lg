package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/daemon"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/faultfs"
)

func noWarning(err error) { Fail(err.Error()) }

var _ = Describe("LockInstance", Label("daemon"), func() {
	var state string

	BeforeEach(func() {
		state = GinkgoT().TempDir()
	})

	It("records the holder's pid in state/daemon.pid", func() {
		held, err := daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)

		Expect(os.ReadFile(filepath.Join(state, "daemon.pid"))).To(Equal(fmt.Appendf(nil, "%d\n", os.Getpid())))
	})

	It("refuses a second holder and reports the pid from state/daemon.pid", func() {
		held, err := daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		Expect(os.WriteFile(filepath.Join(state, "daemon.pid"), []byte("4242\n"), 0o644)).To(Succeed())

		_, err = daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)

		Expect(err).To(MatchError("already running (pid 4242)"))
	})

	It("reports the pid that state/daemon.lock records when state/daemon.pid is missing", func() {
		held, err := daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		Expect(os.Remove(filepath.Join(state, "daemon.pid"))).To(Succeed())

		_, err = daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)

		Expect(err).To(MatchError(fmt.Sprintf("already running (pid %d)", os.Getpid())))
	})

	It("reports an unknown pid when neither state/daemon.pid nor state/daemon.lock records one", func() {
		held, err := daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		Expect(os.Remove(filepath.Join(state, "daemon.pid"))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(state, "daemon.lock"), nil, 0o644)).To(Succeed())

		_, err = daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)

		Expect(err).To(MatchError("already running (pid unknown)"))
	})

	It("holds the lock and warns when it cannot write state/daemon.pid, as on a full disk, and removes a stale one", func() {
		Expect(os.WriteFile(filepath.Join(state, "daemon.pid"), []byte("4242\n"), 0o644)).To(Succeed())
		fsys := faultfs.New()
		fsys.FailOnUnder("create", state, syscall.ENOSPC)
		var warnings []error

		held, err := daemon.LockInstance(fsys, state, clock.Real{}, func(err error) { warnings = append(warnings, err) })

		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		Expect(warnings).To(ConsistOf(MatchError(syscall.ENOSPC)))
		_, err = daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)
		Expect(err).To(MatchError(fmt.Sprintf("already running (pid %d)", os.Getpid())))
	})

	It("waits out a shared lock that lg status takes for a moment", func() {
		file, err := os.Create(filepath.Join(state, "daemon.lock"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(file.Close)
		Expect(syscall.Flock(int(file.Fd()), syscall.LOCK_SH)).To(Succeed())
		time.AfterFunc(100*time.Millisecond, func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) })

		held, err := daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)
		Expect(err).NotTo(HaveOccurred())
		Expect(held.Release()).To(Succeed())
	})

	It("makes Running report true while held", func() {
		Expect(daemon.Running(state)).To(BeFalse())
		held, err := daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)
		Expect(err).NotTo(HaveOccurred())
		Expect(daemon.Running(state)).To(BeTrue())

		Expect(held.Release()).To(Succeed())
		Expect(daemon.Running(state)).To(BeFalse())
	})

	It("removes state/daemon.pid through its FS on Release", func() {
		fsys := faultfs.New()
		held, err := daemon.LockInstance(fsys, state, clock.Real{}, noWarning)
		Expect(err).NotTo(HaveOccurred())

		Expect(held.Release()).To(Succeed())
		Expect(fsys.Journal()).To(ContainElement(faultfs.Op{Name: "remove", Path: filepath.Join(state, "daemon.pid")}))
	})

	It("lets the next holder in after Release, which removes state/daemon.pid", func() {
		held, err := daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)
		Expect(err).NotTo(HaveOccurred())
		Expect(held.Release()).To(Succeed())
		Expect(filepath.Join(state, "daemon.pid")).NotTo(BeAnExistingFile())

		next, err := daemon.LockInstance(store.OSFS{}, state, clock.Real{}, noWarning)
		Expect(err).NotTo(HaveOccurred())
		Expect(next.Release()).To(Succeed())
	})
})
