package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
)

const (
	// instanceWait outlasts the moment lock.Held holds a lock, so lg status
	// never makes a daemon think another one runs.
	instanceWait = time.Second
	requestWait  = 10 * time.Second
)

// InstanceLock is state/daemon.lock, which one daemon holds for its life,
// and state/daemon.pid, which names it.
type InstanceLock struct {
	held *lock.Lock
	pid  string
}

func LockInstance(state string, clk clock.Clock) (*InstanceLock, error) {
	pid := filepath.Join(state, "daemon.pid")
	held, err := lock.Wait(filepath.Join(state, "daemon.lock"), instanceWait, clk, func(string) {})
	if errors.Is(err, lock.ErrTimeout) {
		return nil, fmt.Errorf("already running (%s)", pidIn(pid))
	}
	if err != nil {
		return nil, err
	}
	if err := store.ReplaceFileFS(store.OSFS{}, pid, fmt.Appendf(nil, "%d\n", os.Getpid())); err != nil {
		return nil, errors.Join(err, held.Release())
	}
	return &InstanceLock{held: held, pid: pid}, nil
}

func pidIn(path string) string {
	content, _ := os.ReadFile(path)
	if pid, err := strconv.Atoi(string(bytes.TrimSpace(content))); err == nil {
		return "pid " + strconv.Itoa(pid)
	}
	return "pid unknown"
}

func (l *InstanceLock) Release() error {
	err := os.Remove(l.pid)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return errors.Join(err, l.held.Release())
}

// Request adds a sync request to state/sync-request under state/request.lock,
// and gives its number.
func Request(state string, clk clock.Clock) (int64, error) {
	held, err := lock.Wait(filepath.Join(state, "request.lock"), requestWait, clk, func(string) {})
	if err != nil {
		return 0, err
	}
	defer func() { _ = held.Release() }()
	n, err := Requested(state)
	if err != nil {
		return 0, err
	}
	n++
	return n, store.ReplaceFileFS(store.OSFS{}, requestFile(state), fmt.Appendf(nil, "%d\n", n))
}

// Requested gives the number of the latest sync request, or 0 before the first.
func Requested(state string) (int64, error) {
	path := requestFile(state)
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(string(bytes.TrimSpace(content)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return n, nil
}

func requestFile(state string) string { return filepath.Join(state, "sync-request") }
