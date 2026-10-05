package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
