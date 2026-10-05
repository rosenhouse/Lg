package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
)

// loadConfig resolves the store's roots and loads config.yaml from env.
func loadConfig(env map[string]string) (config.Roots, config.Config, error) {
	roots, err := config.Locations(env)
	if err != nil {
		return config.Roots{}, config.Config{}, err
	}
	file, err := config.File(env)
	if err != nil {
		return config.Roots{}, config.Config{}, err
	}
	cfg, err := config.Load(file)
	return roots, cfg, err
}

// writeLockWait bounds how long a writer waits for another to finish.
const writeLockWait = 5 * time.Minute

// openForWriting takes state/write.lock, which every writer of data/ and tmp/
// holds, waiting up to timeout, then initializes the store and sweeps what
// dead writers left in tmp/.
func openForWriting(roots config.Roots, deps *Deps, timeout time.Duration) (*store.Store, func(), error) {
	if err := os.MkdirAll(roots.State, 0o755); err != nil {
		return nil, nil, err
	}
	writeLock := filepath.Join(roots.State, "write.lock")
	held, err := lock.Wait(writeLock, timeout, deps.Clock, func(holder string) {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: waiting for %s (held by %s)\n", writeLock, holder)
	})
	if err != nil {
		return nil, nil, err
	}
	s, err := initAndSweep(deps.StoreFS, roots.Store)
	if err != nil {
		_ = held.Release()
		return nil, nil, err
	}
	return s, func() { _ = held.Release() }, nil
}

func initAndSweep(fsys store.FS, root string) (*store.Store, error) {
	if err := store.InitFS(fsys, root); err != nil {
		return nil, err
	}
	s, err := store.OpenFS(fsys, root)
	if err != nil {
		return nil, err
	}
	return s, s.Sweep()
}
