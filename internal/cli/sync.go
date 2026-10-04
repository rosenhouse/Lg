package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rosenhouse/lg/internal/auth"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/failure"
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
	if config.IsLoopback(cfg.APIURL) && deps.Env["LG_GH"] == "" {
		return config.Error(fmt.Sprintf("api_url may be on a loopback address only when LG_GH is set: %q", cfg.APIURL))
	}
	api, err := github.BaseURL(cfg.Host, cfg.APIURL)
	if err != nil {
		return err
	}
	s, release, err := openForWriting(roots, deps)
	if err != nil {
		return failure.FromErrno(err)
	}
	defer release()
	m := mirror.Mirror{
		Tokens: auth.GhTokenSource{Runner: deps.Runner, Env: deps.Env},
		NewGitHub: func(token string) github.Client {
			return deps.NewGitHub(api, cfg.Repo, token, deps.Clock)
		},
		Store:            s,
		State:            roots.State,
		Host:             cfg.Host,
		Repo:             cfg.Repo,
		Clock:            deps.Clock,
		LogGrace:         time.Duration(cfg.LogGrace),
		ArtifactMaxBytes: int64(cfg.ArtifactMaxBytes),
	}
	// Ending ctx on a signal kills gh's process group, which the signal does not reach.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return m.Cycle(ctx)
}

// writeLockWait bounds how long a writer waits for another to finish.
const writeLockWait = 5 * time.Minute

// openForWriting takes state/write.lock, which every writer of data/ and tmp/
// holds, then initializes the store and sweeps what dead writers left in tmp/.
func openForWriting(roots config.Roots, deps *Deps) (*store.Store, func(), error) {
	if err := os.MkdirAll(roots.State, 0o755); err != nil {
		return nil, nil, err
	}
	writeLock := filepath.Join(roots.State, "write.lock")
	held, err := lock.Wait(writeLock, writeLockWait, deps.Clock, func(holder string) {
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
