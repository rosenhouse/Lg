package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rosenhouse/lg/internal/auth"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/status"
	"github.com/rosenhouse/lg/internal/store"
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
	held, err := lockWrites(roots, deps, writeLockWait)
	if err != nil {
		return failure.FromErrno(err)
	}
	defer func() { _ = held.Release() }()
	// Ending ctx on a signal kills gh's process group, which the signal does not reach.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	started := deps.Clock.Now()
	report, err := runCycle(ctx, roots, cfg, api, deps)
	return errors.Join(err, writeStatus(deps.StoreFS, roots, cfg, status.Cycle{
		Started:       started,
		Finished:      deps.Clock.Now(),
		Err:           err,
		Completed:     report.Completed,
		Pending:       report.Pending,
		DefaultBranch: report.DefaultBranch,
	}))
}

// runCycle opens the store and runs one cycle. Callers hold state/write.lock.
func runCycle(ctx context.Context, roots config.Roots, cfg config.Config, api *url.URL, deps *Deps) (mirror.Report, error) {
	s, err := initAndSweep(deps.StoreFS, roots.Store)
	if err != nil {
		return mirror.Report{}, failure.FromErrno(err)
	}
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
	return m.Cycle(ctx)
}

// writeStatus completes c from cfg and the disk, and writes it over
// state/status.json. Callers hold state/write.lock.
func writeStatus(fsys store.FS, roots config.Roots, cfg config.Config, c status.Cycle) error {
	path := filepath.Join(roots.State, "status.json")
	// Main's warning named a status.json that Read fails on, and Write replaces it.
	prev, _ := status.Read(path)
	c.Repo = cfg.Host + "/" + cfg.Repo
	c.SyncInterval, c.Retention, c.DiskCap = time.Duration(cfg.SyncInterval), time.Duration(cfg.Retention), int64(cfg.DiskCap)
	var err error
	c.Disk, err = status.Measure(roots.Data, roots.State, c.Repo)
	return failure.FromErrno(errors.Join(err, status.Write(fsys, path, status.Next(prev, c))))
}
