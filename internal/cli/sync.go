package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rosenhouse/lg/internal/auth"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
)

type syncCmd struct{}

func (syncCmd) Run(deps *Deps) error {
	roots, cfg, err := loadConfig(deps.Env)
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
	s, release, err := openForWriting(roots, deps, writeLockWait)
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
		Host:             cfg.Host,
		Repo:             cfg.Repo,
		Clock:            deps.Clock,
		LogGrace:         time.Duration(cfg.LogGrace),
		ArtifactMaxBytes: int64(cfg.ArtifactMaxBytes),
		Backfill:         time.Duration(cfg.Backfill),
		Retention:        time.Duration(cfg.Retention),
		DiskCap:          int64(cfg.DiskCap),
	}
	// Ending ctx on a signal kills gh's process group, which the signal does not reach.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	return m.Cycle(ctx)
}
