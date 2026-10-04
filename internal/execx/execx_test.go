package execx_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/execx"
)

var _ = Describe("Real", Label("transport"), func() {
	It("runs a program in exactly the env given, returning its stdout, stderr and exit status", func() {
		stdout, stderr, err := execx.Real{}.Run(context.Background(), "sh",
			[]string{"-c", `echo "$X ${HOME:-unset}"; echo oops >&2; exit 3`},
			map[string]string{"X": "x", "PATH": os.Getenv("PATH")})

		Expect(string(stdout)).To(Equal("x unset\n"))
		Expect(string(stderr)).To(Equal("oops\n"))
		var exitErr *exec.ExitError
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.ExitCode()).To(Equal(3))
	})

	It("returns soon after ctx ends even while the program's child holds stdout", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		DeferCleanup(cancel)
		args, env := withStdoutHolder("wait")
		done := make(chan error, 1)
		go func() {
			_, _, err := execx.Real{}.Run(ctx, "sh", args, env)
			done <- err
		}()

		var err error
		Eventually(done, "5s").Should(Receive(&err))
		Expect(err).To(HaveOccurred())
	})

	It("kills the program's children too when ctx ends", Label("blocked"), func() {
		alive := filepath.Join(GinkgoT().TempDir(), "alive")
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		DeferCleanup(cancel)

		_, _, err := execx.Real{}.Run(ctx, "sh", []string{"-c", `(sleep 1; touch "$ALIVE") & wait`},
			map[string]string{"PATH": os.Getenv("PATH"), "ALIVE": alive})

		Expect(err).To(HaveOccurred())
		Consistently(alive, "2s").ShouldNot(BeAnExistingFile())
	})

	It("returns the stdout of a program that exits 0 while its child holds stdout", func() {
		args, env := withStdoutHolder("echo lg-test-token")

		stdout, _, err := execx.Real{}.Run(context.Background(), "sh", args, env)

		Expect(err).NotTo(HaveOccurred())
		Expect(string(stdout)).To(Equal("lg-test-token\n"))
	})
})

// withStdoutHolder gives sh args that start a child holding stdout and then
// run script. The child is killed when the spec ends.
func withStdoutHolder(script string) (args []string, env map[string]string) {
	pidFile := filepath.Join(GinkgoT().TempDir(), "pid")
	DeferCleanup(func() {
		var pid int
		Eventually(func() (err error) {
			data, err := os.ReadFile(pidFile)
			if err == nil {
				pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			}
			return err
		}).Should(Succeed())
		child, err := os.FindProcess(pid)
		Expect(err).NotTo(HaveOccurred())
		_ = child.Kill()
	})
	args = []string{"-c", `sleep 60 & echo $! > "$PIDFILE"; ` + script}
	return args, map[string]string{"PATH": os.Getenv("PATH"), "PIDFILE": pidFile}
}
