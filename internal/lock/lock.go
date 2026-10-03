// Package lock takes exclusive flocks on files under state/.
package lock

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
)

const pollInterval = 50 * time.Millisecond

// Lock is an exclusive flock, released by Release or when the process exits.
type Lock struct {
	file *os.File
}

// Wait takes the lock on path, polling until timeout, and then records this process's pid in it.
func Wait(path string, timeout time.Duration, clk clock.Clock) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := clk.After(timeout)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = file.Close()
			return nil, &os.PathError{Op: "flock", Path: path, Err: err}
		}
		select {
		case <-deadline:
			_ = file.Close()
			return nil, fmt.Errorf("%s is held by pid %s; gave up after %s", path, holder(path), timeout)
		case <-clk.After(pollInterval):
		}
	}
	if err := writePID(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Lock{file: file}, nil
}

func writePID(file *os.File) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	_, err := file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return err
}

func holder(path string) string {
	pid, _ := os.ReadFile(path)
	return string(bytes.TrimSpace(pid))
}

func (l *Lock) Release() error { return l.file.Close() }
