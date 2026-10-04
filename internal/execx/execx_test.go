package execx_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
		done := make(chan error, 1)
		go func() {
			_, _, err := execx.Real{}.Run(ctx, "sh", []string{"-c", "sleep 60; :"}, map[string]string{"PATH": os.Getenv("PATH")})
			done <- err
		}()

		var err error
		Eventually(done, "5s").Should(Receive(&err))
		Expect(err).To(HaveOccurred())
	})
})
