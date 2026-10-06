// Package auth gets the token lg sends to GitHub.
package auth

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rosenhouse/lg/internal/execx"
	"github.com/rosenhouse/lg/internal/failure"
)

type TokenSource interface {
	Token(ctx context.Context, host string) (string, error)
}

// GhTokenSource asks gh, or the program LG_GH names, for host's token. It
// kills gh after Timeout, or 30s when Timeout is 0, since a gh waiting on a
// keyring prompt would never exit. Every failure but ctx's end blocks the
// cycle as auth.
type GhTokenSource struct {
	Runner  execx.Runner
	Env     map[string]string
	Timeout time.Duration
}

func (g GhTokenSource) Token(ctx context.Context, host string) (string, error) {
	token, err := g.token(ctx, host)
	switch {
	case err == nil:
		return token, nil
	case ctx.Err() != nil:
		return "", ctx.Err()
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		return "", failure.Blocked{Kind: failure.Auth, Detail: fmt.Sprintf("%s; install gh or set LG_GH to its path", err)}
	case errors.Is(err, errHung), errors.As(err, new(keyringError)):
		// gh hangs on, or fails to reach, an OS keyring, as under launchd or systemd.
		return "", failure.Blocked{Kind: failure.Auth, Detail: fmt.Sprintf("%s; run `gh auth login --hostname %s --insecure-storage`", err, host)}
	case errors.As(err, new(noTokenError)):
		// gh says this too when its keyring is out of reach.
		return "", failure.Blocked{Kind: failure.Auth, Detail: fmt.Sprintf("%s; run `gh auth login --hostname %s`, adding `--insecure-storage` if `gh auth token` works in your shell, since then gh's token is in a keyring that lg cannot reach", err, host)}
	}
	return "", failure.Blocked{Kind: failure.Auth, Detail: fmt.Sprintf("%s; run `gh auth login --hostname %s`", err, host)}
}

var errHung = errors.New("did not exit")

// maxStderr bounds the gh stderr in the detail that every command warns with.
const maxStderr = 512

// truncate cuts s to at most n bytes at a rune boundary, marking a cut with "…".
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// keyringError is a gh failure whose stderr names a keyring service.
type keyringError struct{ error }

func (k keyringError) Unwrap() error { return k.error }

var keyringWords = []string{"keyring", "keychain", "dbus", "secret service", "org.freedesktop.secrets"}

// noTokenError is a gh failure that found no token for the host.
type noTokenError struct{ error }

func (n noTokenError) Unwrap() error { return n.error }

func (g GhTokenSource) token(ctx context.Context, host string) (string, error) {
	gh := cmp.Or(g.Env["LG_GH"], "gh")
	args := []string{"auth", "token", "--hostname", host}
	command := gh + " " + strings.Join(args, " ")
	timeout := cmp.Or(g.Timeout, 30*time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, err := g.Runner.Run(ctx, gh, args, g.Env)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("%s %w within %s", command, errHung, timeout)
	}
	if err != nil {
		if msg := bytes.TrimSpace(stderr); len(msg) > 0 {
			err = fmt.Errorf("%w: %s", err, truncate(string(msg), maxStderr))
			lower := strings.ToLower(string(msg))
			switch {
			case slices.ContainsFunc(keyringWords, func(word string) bool { return strings.Contains(lower, word) }):
				err = keyringError{err}
			case strings.Contains(lower, "no oauth token found"):
				err = noTokenError{err}
			}
		}
		return "", fmt.Errorf("%s: %w", command, err)
	}
	token := string(bytes.TrimSpace(stdout))
	if token == "" {
		return "", fmt.Errorf("%s printed no token", command)
	}
	if strings.ContainsFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", fmt.Errorf("%s printed more than a token", command)
	}
	return token, nil
}
