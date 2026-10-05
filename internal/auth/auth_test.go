package auth_test

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/auth"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/execx"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/testsupport/fakegh"
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

	DescribeTable("blocks as auth, naming the command, any stderr from gh but not its stdout, and `gh auth login`, when gh fails", Label("blocked"),
		func(stderr, detail string) {
			runner := &fakeRunner{stdout: "gho_partial", stderr: stderr, err: errors.New("exit status 1")}

			_, err := auth.GhTokenSource{Runner: runner}.Token(context.Background(), "ghe.corp.example")

			Expect(err).To(Equal(failure.Blocked{Kind: failure.Auth, Detail: detail + "; run `gh auth login --hostname ghe.corp.example`"}))
		},
		Entry("with stderr", "not logged in\n", "gh auth token --hostname ghe.corp.example: exit status 1: not logged in"),
		Entry("without stderr", " \n", "gh auth token --hostname ghe.corp.example: exit status 1"),
	)

	It("blocks as auth suggesting `--insecure-storage` when gh's stderr mentions the keyring", Label("status"), func() {
		runner := &fakeRunner{stderr: "failed to get token from the Keyring: dbus: no session bus\n", err: errors.New("exit status 1")}

		_, err := auth.GhTokenSource{Runner: runner}.Token(context.Background(), "github.com")

		Expect(err).To(Equal(failure.Blocked{Kind: failure.Auth, Detail: "gh auth token --hostname github.com: exit status 1: failed to get token from the Keyring: dbus: no session bus; run `gh auth login --hostname github.com --insecure-storage`"}))
	})

	DescribeTable("blocks as auth, saying to install gh or set LG_GH, when gh cannot be started", Label("blocked"),
		func(startErr error) {
			runner := &fakeRunner{err: startErr}

			_, err := auth.GhTokenSource{Runner: runner}.Token(context.Background(), "github.com")

			Expect(err).To(Equal(failure.Blocked{Kind: failure.Auth, Detail: "gh auth token --hostname github.com: " + startErr.Error() + "; install gh or set LG_GH to its path"}))
		},
		Entry("not found", &exec.Error{Name: "gh", Err: exec.ErrNotFound}),
		Entry("not executable", &fs.PathError{Op: "fork/exec", Path: "/some/dir", Err: syscall.EACCES}),
		Entry("missing", &fs.PathError{Op: "fork/exec", Path: "/no/gh", Err: syscall.ENOENT}),
	)

	DescribeTable("blocks as auth, without showing the output, when gh prints no token or more than a token", Label("blocked"),
		func(stdout, message string) {
			_, err := auth.GhTokenSource{Runner: &fakeRunner{stdout: stdout}}.Token(context.Background(), "github.com")

			Expect(err).To(Equal(failure.Blocked{Kind: failure.Auth, Detail: "gh auth token --hostname github.com " + message + "; run `gh auth login --hostname github.com`"}))
		},
		Entry("nothing", "\n", "printed no token"),
		Entry("two lines", "notice\ngho_secret\n", "printed more than a token"),
		Entry("a space", "gho_secret extra\n", "printed more than a token"),
		Entry("a control character", "gho_\x1bsecret\n", "printed more than a token"),
	)
})

// deadlineRunner records the deadline of the context it runs gh in.
type deadlineRunner struct {
	deadline time.Time
	ok       bool
}

func (d *deadlineRunner) Run(ctx context.Context, _ string, _ []string, _ map[string]string) (stdout, stderr []byte, err error) {
	d.deadline, d.ok = ctx.Deadline()
	return []byte("gho_secret\n"), nil, nil
}

var _ = Describe("GhTokenSource's timeout", Label("blocked"), func() {
	It("is 30s by default", func() {
		runner := &deadlineRunner{}

		before := clock.Real{}.Now()
		_, err := auth.GhTokenSource{Runner: runner}.Token(context.Background(), "github.com")
		after := clock.Real{}.Now()

		Expect(err).NotTo(HaveOccurred())
		Expect(runner.ok).To(BeTrue())
		Expect(runner.deadline).To(BeTemporally(">=", before.Add(30*time.Second)))
		Expect(runner.deadline).To(BeTemporally("<=", after.Add(30*time.Second)))
	})

	It("kills a gh that has not exited, and blocks as auth naming the timeout and `--insecure-storage`", func(ctx SpecContext) {
		gh := fakegh.New(GinkgoT().TempDir())
		gh.Hang()

		_, err := auth.GhTokenSource{Runner: execx.Real{}, Env: map[string]string{"LG_GH": gh.Path}, Timeout: 200 * time.Millisecond}.Token(ctx, "github.com")

		Expect(err).To(Equal(failure.Blocked{Kind: failure.Auth, Detail: gh.Path + " auth token --hostname github.com did not exit within 200ms; run `gh auth login --hostname github.com --insecure-storage`"}))
	}, SpecTimeout(10*time.Second))

	It("returns ctx's error, not Blocked, when ctx ends first", func(ctx SpecContext) {
		gh := fakegh.New(GinkgoT().TempDir())
		gh.Hang()
		cancelled, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		DeferCleanup(cancel)

		_, err := auth.GhTokenSource{Runner: execx.Real{}, Env: map[string]string{"LG_GH": gh.Path}}.Token(cancelled, "github.com")

		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(errors.As(err, new(failure.Blocked))).To(BeFalse())
	}, SpecTimeout(10*time.Second))
})
