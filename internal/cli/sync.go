package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rosenhouse/lg/internal/auth"
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
	if config.IsLoopback(cfg.APIURL) && deps.Env["LG_GH"] == "" {
		return config.Error(fmt.Sprintf("api_url may be on a loopback address only when LG_GH is set: %q", cfg.APIURL))
	}
	api, err := github.BaseURL(cfg.Host, cfg.APIURL)
	if err != nil {
		return err
	}
	s, release, err := openForWriting(roots, deps.Clock, deps.Stderr)
	if err != nil {
		return err
	}
	defer release()
	client := github.NewHTTPClient(github.DefaultTimeouts)
	m := mirror.Mirror{
		Tokens: auth.GhTokenSource{Runner: deps.Runner, Env: deps.Env},
		NewGitHub: func(token string) github.Client {
			return github.NewHTTP(client, api, cfg.Repo, token)
		},
		Store:    s,
		Host:     cfg.Host,
		Repo:     cfg.Repo,
		Clock:    deps.Clock,
		LogGrace: cfg.LogGrace,
	}
	return m.Cycle(context.Background())
}

// writeLockWait bounds how long a writer waits for another to finish.
const writeLockWait = 5 * time.Minute

// openForWriting takes state/write.lock, which every writer of data/ and tmp/
// holds, then initializes the store and sweeps what dead writers left in tmp/.
func openForWriting(roots config.Roots, clk clock.Clock, stderr io.Writer) (*store.Store, func(), error) {
	if err := os.MkdirAll(roots.State, 0o755); err != nil {
		return nil, nil, err
	}
	writeLock := filepath.Join(roots.State, "write.lock")
	held, err := lock.Wait(writeLock, writeLockWait, clk, func(holder string) {
		_, _ = fmt.Fprintf(stderr, "lg: waiting for %s (held by %s)\n", writeLock, holder)
	})
	if err != nil {
		return nil, nil, err
	}
	s, err := initAndSweep(roots.Store)
	if err != nil {
		_ = held.Release()
		return nil, nil, err
	}
	return s, func() { _ = held.Release() }, nil
}

func initAndSweep(root string) (*store.Store, error) {
	if err := store.Init(root); err != nil {
		return nil, err
	}
	s, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	return s, s.Sweep()
}
