// Package lock takes exclusive flocks on files under state/.
package lock

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
)

const pollInterval = 50 * time.Millisecond

// ErrTimeout is wrapped by Wait's error when the lock stays busy past its timeout.
var ErrTimeout = errors.New("gave up")

// Lock is an exclusive flock, released by Release or when the process exits.
type Lock struct {
	file *os.File
}

// Wait takes the lock on path, polling until timeout, and then records this
// process's pid in it if it can. If the lock is busy, it first calls waiting
// with the holder.
func Wait(path string, timeout time.Duration, clk clock.Clock, waiting func(holder string)) (*Lock, error) {
	return WaitContext(context.Background(), path, timeout, clk, waiting)
}

// WaitContext is Wait, giving up with ctx's error when ctx is done first.
func WaitContext(ctx context.Context, path string, timeout time.Duration, clk clock.Clock, waiting func(holder string)) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := clk.After(timeout)
	for tries := 0; ; tries++ {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = file.Close()
			return nil, &os.PathError{Op: "flock", Path: path, Err: err}
		}
		if tries == 0 {
			waiting(holder(path))
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-deadline:
			_ = file.Close()
			return nil, fmt.Errorf("%s is held by %s; %w after %s", path, holder(path), ErrTimeout, timeout)
		case <-clk.After(pollInterval):
		}
	}
	// The pid only names the holder to waiters, so a full disk, where gc
	// must still take the lock to free space, leaves it unknown.
	_ = file.Truncate(0)
	_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return &Lock{file: file}, nil
}

func holder(path string) string {
	content, _ := os.ReadFile(path)
	pid := string(bytes.TrimSpace(content))
	if _, err := strconv.Atoi(pid); err != nil {
		return "another process (pid unknown)"
	}
	return "pid " + pid
}

// Release clears the pid, so no waiter names a holder that has let go.
func (l *Lock) Release() error {
	_ = l.file.Truncate(0)
	return l.file.Close()
}

// Held reports whether a holder has the lock on path. It takes a shared
// lock for a moment, so a Wait that overlaps it needs a timeout longer than
// one poll.
func Held(path string) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	if err != nil {
		return false, &os.PathError{Op: "flock", Path: path, Err: err}
	}
	return false, nil
}
