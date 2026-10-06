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
	"github.com/rosenhouse/lg/internal/status"
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

// LockInstance takes state/daemon.lock and writes state/daemon.pid. It
// passes a failure to write daemon.pid to warn, since a full disk must not
// stop the daemon whose retention frees space.
func LockInstance(fsys store.FS, state string, clk clock.Clock, warn func(error)) (*InstanceLock, error) {
	pid := filepath.Join(state, "daemon.pid")
	held, err := lock.Wait(instanceFile(state), instanceWait, clk, func(string) {})
	if errors.Is(err, lock.ErrTimeout) {
		return nil, fmt.Errorf("already running (%s)", runningPID(state))
	}
	if err != nil {
		return nil, err
	}
	if err := store.ReplaceFileFS(fsys, pid, fmt.Appendf(nil, "%d\n", os.Getpid())); err != nil {
		warn(errors.Join(err, fsys.RemoveAll(pid)))
	}
	return &InstanceLock{held: held, pid: pid}, nil
}

// Running reports whether a daemon holds state/daemon.lock.
func Running(state string) (bool, error) { return lock.Held(instanceFile(state)) }

func instanceFile(state string) string { return filepath.Join(state, "daemon.lock") }

// runningPID names the daemon by state/daemon.pid, else by the pid
// state/daemon.lock records.
func runningPID(state string) string {
	for _, name := range []string{"daemon.pid", "daemon.lock"} {
		content, _ := os.ReadFile(filepath.Join(state, name))
		if pid, err := strconv.Atoi(string(bytes.TrimSpace(content))); err == nil {
			return "pid " + strconv.Itoa(pid)
		}
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
// and gives its number. The number follows status.json's served_request too,
// so a removed or garbled state/sync-request never makes a request look served.
func Request(state string, clk clock.Clock) (int64, error) {
	held, err := lock.Wait(filepath.Join(state, "request.lock"), requestWait, clk, func(string) {})
	if err != nil {
		return 0, err
	}
	defer func() { _ = held.Release() }()
	n, _ := Requested(state)
	if st, _ := status.Read(filepath.Join(state, "status.json")); st != nil {
		n = max(n, st.ServedRequest)
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
