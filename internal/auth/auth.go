// Package auth gets the token lg sends to GitHub.
package auth

import (
	"context"

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

func (g GhTokenSource) Token(ctx context.Context, host string) (string, error) { return "", nil }
