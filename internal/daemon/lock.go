package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
)

// instanceWait outlasts the moment lock.Held holds a lock, so lg status
// never makes a daemon think another one runs.
const instanceWait = time.Second

// InstanceLock is state/daemon.lock, which one daemon holds for its life,
// and state/daemon.pid, which names it.
type InstanceLock struct {
	held *lock.Lock
	fsys store.FS
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
	return &InstanceLock{held: held, fsys: fsys, pid: pid}, nil
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
	return errors.Join(l.fsys.RemoveAll(l.pid), l.held.Release())
}
