package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
)

var _ = Describe("lg sync", Label("store"), func() {
	It("exits 4 after waiting 5m for a busy state/write.lock", func() {
		home := GinkgoT().TempDir()
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
		writeLock := filepath.Join(home, "state", "write.lock")
		Expect(os.Mkdir(filepath.Dir(writeLock), 0o755)).To(Succeed())
		held, err := lock.Wait(writeLock, time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)

		var stderr bytes.Buffer
		clk := &firedClock{}
		code := make(chan int, 1)
		go func() {
			code <- cli.Main([]string{"sync"}, cli.Deps{
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
})

var _ = Describe("lg sync with a loopback api_url", Label("transport"), func() {
	It("exits 2 and runs no gh unless LG_GH names a test gh", func() {
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\napi_url: http://127.0.0.1:1\n"), 0o644)).To(Succeed())
		var stderr bytes.Buffer
		runner := &countingRunner{}

		code := cli.Main([]string{"sync"}, cli.Deps{
			Env:    map[string]string{"LG_HOME": GinkgoT().TempDir(), "LG_CONFIG": config},
			Stdout: &bytes.Buffer{},
			Stderr: &stderr,
			Clock:  clock.Real{},
			Runner: runner,
		})

		Expect(code).To(Equal(2))
		Expect(stderr.String()).To(Equal("lg: api_url may be on a loopback address only when LG_GH is set: \"http://127.0.0.1:1\"\n"))
		Expect(runner.runs).To(BeZero())
	})
})

type countingRunner struct{ runs int }

func (r *countingRunner) Run(context.Context, string, []string, map[string]string) (stdout, stderr []byte, err error) {
	r.runs++
	return nil, nil, fmt.Errorf("countingRunner runs nothing")
}

// firedClock fires every After at once, recording the durations asked for.
type firedClock struct {
	clock.Real
	mu        sync.Mutex
	durations []time.Duration
}

func (c *firedClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.durations = append(c.durations, d)
	fired := make(chan time.Time, 1)
	fired <- time.Time{}
	return fired
}

func (c *firedClock) requested() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.durations...)
}
