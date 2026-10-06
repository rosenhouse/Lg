package cli

import (
	"context"
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

// noStore is the error for a command that needs a store at a root without one,
// which is likely a mistyped LG_HOME.
func noStore(roots config.Roots) error {
	return config.Error(fmt.Sprintf("%s holds no lg store; check LG_HOME", roots.Store))
}

// writeLockWait bounds how long a writer waits for another to finish.
const writeLockWait = 5 * time.Minute

// openForWriting takes state/write.lock, as lockWrites does. Then it runs
// check, unless it is nil, initializes the store and sweeps what dead writers
// left in tmp/.
func openForWriting(roots config.Roots, deps *Deps, timeout time.Duration, check func() error) (*store.Store, func(), error) {
	held, err := lockWrites(context.Background(), roots, deps, timeout)
	if err != nil {
		return nil, nil, err
	}
	if check != nil {
		err = check()
	}
	var s *store.Store
	if err == nil {
		s, err = initAndSweep(deps.StoreFS, roots.Store)
	}
	if err != nil {
		_ = held.Release()
		return nil, nil, err
	}
	return s, func() { _ = held.Release() }, nil
}

// lockWrites takes state/write.lock, which every writer of data/ and tmp/
// holds, waiting up to timeout or until ctx is done.
func lockWrites(ctx context.Context, roots config.Roots, deps *Deps, timeout time.Duration) (*lock.Lock, error) {
	if err := os.MkdirAll(roots.State, 0o755); err != nil {
		return nil, err
	}
	writeLock := filepath.Join(roots.State, "write.lock")
	return lock.WaitContext(ctx, writeLock, timeout, deps.Clock, func(holder string) {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: waiting for %s (held by %s)\n", writeLock, holder)
	})
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

func validateTimeout(timeout time.Duration) error {
	if timeout < 0 {
		return fmt.Errorf("--timeout must not be negative: %s", timeout)
	}
	return nil
}
