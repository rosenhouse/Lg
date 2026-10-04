package auth_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/auth"
)

type run struct {
	name string
	args []string
	env  map[string]string
}

// fakeRunner records each run and prints what it is given.
type fakeRunner struct {
	runs           []run
	stdout, stderr string
	err            error
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string, env map[string]string) (stdout, stderr []byte, err error) {
	f.runs = append(f.runs, run{name, args, env})
	return []byte(f.stdout), []byte(f.stderr), f.err
}

var _ = Describe("GhTokenSource", Label("transport"), func() {
	DescribeTable("runs `<LG_GH or gh> auth token --hostname <host>` through Runner and trims the output",
		func(env map[string]string, gh string) {
			runner := &fakeRunner{stdout: "  gho_secret\n"}

			token, err := auth.GhTokenSource{Runner: runner, Env: env}.Token(context.Background(), "ghe.corp.example")

			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(Equal("gho_secret"))
			Expect(runner.runs).To(Equal([]run{{gh, []string{"auth", "token", "--hostname", "ghe.corp.example"}, env}}))
		},
		Entry("LG_GH set", map[string]string{"LG_GH": "/opt/gh/bin/gh", "PATH": "/bin"}, "/opt/gh/bin/gh"),
		Entry("LG_GH unset", map[string]string{"PATH": "/bin"}, "gh"),
		Entry("LG_GH empty", map[string]string{"LG_GH": "", "PATH": "/bin"}, "gh"),
	)

	DescribeTable("names the command and any stderr from gh, but not its stdout, when gh fails",
		func(stderr, message string) {
			runner := &fakeRunner{stdout: "gho_partial", stderr: stderr, err: errors.New("exit status 1")}

			_, err := auth.GhTokenSource{Runner: runner}.Token(context.Background(), "github.com")

			Expect(err).To(MatchError(message))
		},
		Entry("with stderr", "not logged in\n", "gh auth token --hostname github.com: exit status 1: not logged in"),
		Entry("without stderr", " \n", "gh auth token --hostname github.com: exit status 1"),
	)

	It("fails when gh prints no token", func() {
		_, err := auth.GhTokenSource{Runner: &fakeRunner{stdout: "\n"}}.Token(context.Background(), "github.com")

		Expect(err).To(MatchError("gh auth token --hostname github.com printed no token"))
	})
})
