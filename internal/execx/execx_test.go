package execx_test

import (
	"context"
	"errors"
	"os"
	"os/exec"

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
})
