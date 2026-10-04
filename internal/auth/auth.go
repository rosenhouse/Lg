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
	"strings"
	"time"
	"unicode"

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
	}
	return "", failure.Blocked{Kind: failure.Auth, Detail: fmt.Sprintf("%s; run `gh auth login --hostname %s`", err, host)}
}

func (g GhTokenSource) token(ctx context.Context, host string) (string, error) {
	gh := cmp.Or(g.Env["LG_GH"], "gh")
	args := []string{"auth", "token", "--hostname", host}
	command := gh + " " + strings.Join(args, " ")
	timeout := cmp.Or(g.Timeout, 30*time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, err := g.Runner.Run(ctx, gh, args, g.Env)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("%s did not exit within %s", command, timeout)
	}
	if err != nil {
		if msg := bytes.TrimSpace(stderr); len(msg) > 0 {
			err = fmt.Errorf("%w: %s", err, msg)
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
