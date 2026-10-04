// Package execx runs other programs.
package execx

import (
	"bytes"
	"context"
	"os/exec"
)

type Runner interface {
	// Run runs name with args in exactly env, and returns what it printed.
	Run(ctx context.Context, name string, args []string, env map[string]string) (stdout, stderr []byte, err error)
}

type Real struct{}

func (Real) Run(ctx context.Context, name string, args []string, env map[string]string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}
