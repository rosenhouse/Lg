// Package auth gets the token lg sends to GitHub.
package auth

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/rosenhouse/lg/internal/execx"
)

type TokenSource interface {
	Token(ctx context.Context, host string) (string, error)
}

// GhTokenSource asks gh, or the program LG_GH names, for host's token.
type GhTokenSource struct {
	Runner execx.Runner
	Env    map[string]string
}

func (g GhTokenSource) Token(ctx context.Context, host string) (string, error) {
	gh := cmp.Or(g.Env["LG_GH"], "gh")
	args := []string{"auth", "token", "--hostname", host}
	command := gh + " " + strings.Join(args, " ")
	stdout, stderr, err := g.Runner.Run(ctx, gh, args, g.Env)
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", command, err, bytes.TrimSpace(stderr))
	}
	token := string(bytes.TrimSpace(stdout))
	if token == "" {
		return "", fmt.Errorf("%s printed no token", command)
	}
	return token, nil
}
