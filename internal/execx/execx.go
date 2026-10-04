// Package execx runs other programs.
package execx

import "context"

type Runner interface {
	// Run runs name with args in exactly env, and returns what it printed.
	Run(ctx context.Context, name string, args []string, env map[string]string) (stdout, stderr []byte, err error)
}

type Real struct{}

func (Real) Run(ctx context.Context, name string, args []string, env map[string]string) (stdout, stderr []byte, err error) {
	return nil, nil, nil
}
