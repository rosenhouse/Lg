package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
)

var _ = Describe("lg gc", Label("retention"), func() {
	var config string

	BeforeEach(func() {
		config = filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
	})

	It("exits 4 after waiting 5m for a busy state/write.lock", func() {
		home := GinkgoT().TempDir()
		Expect(store.Init(home)).To(Succeed())
		writeLock := filepath.Join(home, "state", "write.lock")
		held, err := lock.Wait(writeLock, time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)

		var stderr bytes.Buffer
		clk := &firedClock{}
		code := make(chan int, 1)
		go func() {
			code <- cli.Main([]string{"gc"}, cli.Deps{
				Env:    map[string]string{"LG_HOME": home, "LG_CONFIG": config},
				Stdout: &bytes.Buffer{},
				Stderr: &stderr,
				Clock:  clk,
			})
		}()

		Eventually(code, 5*time.Second).Should(Receive(Equal(4)))
		Expect(clk.requested()).To(ContainElement(5 * time.Minute))
		Expect(stderr.String()).To(HaveSuffix(fmt.Sprintf("lg: %s is held by pid %d; gave up after 5m0s\n", writeLock, os.Getpid())))
	})

	It("--dry-run prints a run at or before the stored horizon", func() {
		home := GinkgoT().TempDir()
		Expect(store.Init(home)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(home, "state", "horizon.json"), []byte(`{"horizon":"2026-10-01T12:00:00Z"}`), 0o644)).To(Succeed())
		run := filepath.Join(home, "data", "github.com", "rosenhouse", "lg", "runs", "2026-10-01", "9_ci_main")
		Expect(os.MkdirAll(filepath.Join(run, "attempt-1"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(run, "attempt-1", "fetch.json"), []byte(`{"run_created_at":"2026-10-01T12:00:00Z"}`), 0o644)).To(Succeed())
		var stdout bytes.Buffer

		code := cli.Main([]string{"gc", "--dry-run"}, cli.Deps{
			Env:    map[string]string{"LG_HOME": home, "LG_CONFIG": config},
			Stdout: &stdout,
			Stderr: GinkgoWriter,
			Clock:  clock.NewFake(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)),
		})
		Expect(code).To(Equal(0))
		Expect(stdout.String()).To(Equal(run + "\n"))
		Expect(run).To(BeADirectory())
	})

	It("waits for a writer making the store, rather than calling LG_HOME a typo", func() {
		home := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(home, "state"), 0o755)).To(Succeed())
		held, err := lock.Wait(filepath.Join(home, "state", "write.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		stderr := gbytes.NewBuffer()
		code := make(chan int, 1)
		go func() {
			code <- cli.Main([]string{"gc"}, cli.Deps{
				Env:     map[string]string{"LG_HOME": home, "LG_CONFIG": config},
				Stdout:  &bytes.Buffer{},
				Stderr:  stderr,
				Clock:   clock.Real{},
				StoreFS: store.OSFS{},
			})
		}()

		Eventually(stderr, 5*time.Second).Should(gbytes.Say("waiting for"))
		Expect(store.Init(home)).To(Succeed())
		Expect(held.Release()).To(Succeed())
		Eventually(code, 5*time.Second).Should(Receive(Equal(0)))
	})

	It("exits 2 and makes no store when LG_HOME holds only a free state/write.lock", func() {
		home := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(home, "state"), 0o755)).To(Succeed())
		held, err := lock.Wait(filepath.Join(home, "state", "write.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		Expect(held.Release()).To(Succeed())
		var stderr bytes.Buffer

		code := cli.Main([]string{"gc"}, cli.Deps{
			Env:    map[string]string{"LG_HOME": home, "LG_CONFIG": config},
			Stdout: &bytes.Buffer{},
			Stderr: &stderr,
			Clock:  clock.Real{},
		})
		Expect(code).To(Equal(2))
		Expect(stderr.String()).To(ContainSubstring("LG_HOME"))
		Expect(filepath.Join(home, "FORMAT")).NotTo(BeAnExistingFile())
		Expect(filepath.Join(home, "data")).NotTo(BeAnExistingFile())
	})

	DescribeTable("exits 2 naming LG_HOME, and makes nothing, when LG_HOME holds no store",
		func(args ...string) {
			home := filepath.Join(GinkgoT().TempDir(), "typo")
			var stderr bytes.Buffer

			code := cli.Main(append([]string{"gc"}, args...), cli.Deps{
				Env:    map[string]string{"LG_HOME": home, "LG_CONFIG": config},
				Stdout: &bytes.Buffer{},
				Stderr: &stderr,
				Clock:  clock.Real{},
			})
			Expect(code).To(Equal(2))
			Expect(stderr.String()).To(ContainSubstring("LG_HOME"))
			Expect(stderr.String()).To(ContainSubstring(home))
			Expect(home).NotTo(BeAnExistingFile())
		},
		Entry("gc"),
		Entry("gc --dry-run", "--dry-run"),
	)
})
