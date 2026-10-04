// Package execx runs other programs.
package execx

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

type Runner interface {
	// Run runs name with args in exactly env, and returns what it printed.
	Run(ctx context.Context, name string, args []string, env map[string]string) (stdout, stderr []byte, err error)
}

type Real struct{}

func (Real) Run(ctx context.Context, name string, args []string, env map[string]string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// Killing the program's process group also kills any child it started,
	// such as a keyring helper.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// A child the program leaves behind can hold stdout open after it exits or ctx ends.
	cmd.WaitDelay = time.Second
	cmd.Env = []string{}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		// The program exited 0 before ctx ended, so what it printed is complete.
		err = nil
	}
	return out.Bytes(), errOut.Bytes(), err
}
