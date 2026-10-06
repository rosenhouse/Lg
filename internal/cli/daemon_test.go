package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = DescribeTable("a daemon cycle that cannot take state/write.lock", Label("daemon"),
	func(block func(writeLock string), skipped bool) {
		home := GinkgoT().TempDir()
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
		Expect(os.Mkdir(filepath.Join(home, "state"), 0o755)).To(Succeed())
		block(filepath.Join(home, "state", "write.lock"))

		out, err := cli.RunDaemonCycle(context.Background(), cli.Deps{
			Env:     map[string]string{"LG_HOME": home, "LG_CONFIG": config},
			Stdout:  &bytes.Buffer{},
			Stderr:  &bytes.Buffer{},
			Clock:   &firedClock{},
			StoreFS: store.OSFS{},
		}, func() int64 { return 1 })

		Expect(err).NotTo(HaveOccurred())
		Expect(out.Err).To(HaveOccurred())
		Expect(out.Skipped).To(Equal(skipped))
	},
	Entry("is skipped, so the daemon retries a request, when its wait times out", func(writeLock string) {
		held, err := lock.Wait(writeLock, time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
	}, true),
	Entry("counts, so the daemon keeps its schedule, on any other error", func(writeLock string) {
		Expect(os.Mkdir(writeLock, 0o755)).To(Succeed())
	}, false),
)

var _ = Describe("a daemon cycle", Label("sync"), func() {
	It("asks which request it serves once it holds state/write.lock, before it syncs", func() {
		home := GinkgoT().TempDir()
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
		writeLock := filepath.Join(home, "state", "write.lock")
		runner := &countingRunner{}
		var lockedThen bool
		runsThen := -1

		_, err := cli.RunDaemonCycle(context.Background(), cli.Deps{
			Env:     map[string]string{"LG_HOME": home, "LG_CONFIG": config},
			Stdout:  &bytes.Buffer{},
			Stderr:  &bytes.Buffer{},
			Clock:   clock.Real{},
			Runner:  runner,
			StoreFS: store.OSFS{},
		}, func() int64 {
			lockedThen, _ = lock.Held(writeLock)
			runsThen = runner.runs
			return 7
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(lockedThen).To(BeTrue())
		Expect(runsThen).To(BeZero())
		Expect(harness.ReadStatus(filepath.Join(home, "state", "status.json"))).To(HaveKeyWithValue("served_request", 7.0))
	})

	It("records a write.lock it cannot open in status.json, serving the request", func() {
		home := GinkgoT().TempDir()
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
		writeLock := filepath.Join(home, "state", "write.lock")
		Expect(os.MkdirAll(writeLock, 0o755)).To(Succeed())

		out, err := cli.RunDaemonCycle(context.Background(), cli.Deps{
			Env:     map[string]string{"LG_HOME": home, "LG_CONFIG": config},
			Stdout:  &bytes.Buffer{},
			Stderr:  &bytes.Buffer{},
			Clock:   clock.Real{},
			StoreFS: store.OSFS{},
		}, func() int64 { return 7 })

		Expect(err).NotTo(HaveOccurred())
		Expect(out.Err).To(MatchError(ContainSubstring(writeLock)))
		Expect(harness.ReadStatus(filepath.Join(home, "state", "status.json"))).To(And(
			HaveKeyWithValue("served_request", 7.0),
			HaveKeyWithValue("last_sync_errors", ConsistOf(ContainSubstring(writeLock+": is a directory")))))
	})
})
