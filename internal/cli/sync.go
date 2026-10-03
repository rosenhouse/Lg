package cli

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/store"
)

type syncCmd struct{}

func (syncCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	file, err := config.File(deps.Env)
	if err != nil {
		return err
	}
	cfg, err := config.Load(file)
	if err != nil {
		return err
	}
	s, release, err := openForWriting(roots)
	if err != nil {
		return err
	}
	defer release()
	m := mirror.Mirror{
		GitHub: github.NewHTTP(&http.Client{}, github.BaseURL(cfg.Host, cfg.APIURL), cfg.Repo),
		Store:  s,
		Host:   cfg.Host,
		Repo:   cfg.Repo,
	}
	return m.Cycle(context.Background())
}

// writeLockWait bounds how long a writer waits for another to finish.
const writeLockWait = 5 * time.Minute

// openForWriting takes state/write.lock, which every writer of data/ and tmp/
// holds, then initializes the store and sweeps what dead writers left in tmp/.
func openForWriting(roots config.Roots) (s *store.Store, release func(), err error) {
	if err := os.MkdirAll(roots.State, 0o755); err != nil {
		return nil, nil, err
	}
	held, err := lock.Wait(filepath.Join(roots.State, "write.lock"), writeLockWait, clock.Real{})
	if err != nil {
		return nil, nil, err
	}
	release = func() { _ = held.Release() }
	if err := store.Init(roots.Store); err != nil {
		release()
		return nil, nil, err
	}
	if s, err = store.Open(roots.Store); err == nil {
		err = s.Sweep()
	}
	if err != nil {
		release()
		return nil, nil, err
	}
	return s, release, nil
}
