package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/rosenhouse/lg/internal/auth"
	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/daemon"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/status"
	"github.com/rosenhouse/lg/internal/store"
)

type syncCmd struct{}

func (syncCmd) Run(deps *Deps) error {
	t, err := loadTarget(deps.Env)
	if err != nil {
		return err
	}
	running, err := daemon.Running(t.roots.State)
	if err != nil {
		return failure.FromErrno(err)
	}
	if running {
		return requestSync(t.roots, deps)
	}
	held, err := lockWrites(context.Background(), t.roots, deps, writeLockWait)
	if err != nil {
		return failure.FromErrno(err)
	}
	defer func() { _ = held.Release() }()
	// Ending ctx on a signal kills gh's process group, which the signal does not reach.
	ctx, stop := signalContext()
	defer stop()
	_, err = recordCycle(ctx, t, deps, func(*status.Cycle) {})
	return err
}

// requestSync asks the running daemon for a cycle.
func requestSync(roots config.Roots, deps *Deps) error {
	n, err := daemon.Request(roots.State, deps.Clock)
	if err != nil {
		return failure.FromErrno(err)
	}
	_, err = fmt.Fprintf(deps.Stderr, "lg: sent sync request %d to the running daemon\n", n)
	return err
}

// target is what a cycle syncs, from config.yaml.
type target struct {
	roots config.Roots
	cfg   config.Config
	api   *url.URL
}

func loadTarget(env map[string]string) (target, error) {
	roots, cfg, err := loadConfig(env)
	if err != nil {
		return target{}, err
	}
	if config.IsLoopback(cfg.APIURL) && env["LG_GH"] == "" {
		return target{}, config.Error(fmt.Sprintf("api_url may be on a loopback address only when LG_GH is set: %q", cfg.APIURL))
	}
	api, err := github.BaseURL(cfg.Host, cfg.APIURL)
	return target{roots: roots, cfg: cfg, api: api}, err
}

// recordCycle runs one cycle and writes status.json, with the fields that
// complete adds. Callers hold state/write.lock.
func recordCycle(ctx context.Context, t target, deps *Deps, complete func(*status.Cycle)) (status.Cycle, error) {
	c := status.Cycle{Started: deps.Clock.Now()}
	report, err := runCycle(ctx, t.roots, t.cfg, t.api, deps)
	c.Finished, c.Err, c.Completed = deps.Clock.Now(), err, report.Completed
	c.Pending, c.DefaultBranch = pending(report.Pending), report.DefaultBranch
	complete(&c)
	return c, errors.Join(err, writeStatus(deps.StoreFS, t.roots, t.cfg, c))
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

func pending(units []mirror.UnitError) []status.Pending {
	found := make([]status.Pending, len(units))
	for i, u := range units {
		found[i] = status.Pending{Unit: u.Unit, Error: u.Err.Error()}
	}
	return found
}

// writeStatus completes c from cfg and the disk, and writes it over
// state/status.json. Callers hold state/write.lock.
func writeStatus(fsys store.FS, roots config.Roots, cfg config.Config, c status.Cycle) error {
	path := filepath.Join(roots.State, "status.json")
	// Main already warned about an unparsable status.json; Next starts over without it.
	prev, _ := status.Read(path)
	c.Repo = repoKey(cfg)
	c.SyncInterval, c.Retention, c.DiskCap = time.Duration(cfg.SyncInterval), time.Duration(cfg.Retention), int64(cfg.DiskCap)
	var err error
	c.Disk, err = status.Measure(roots.Data, roots.State, c.Repo)
	return failure.FromErrno(errors.Join(err, status.Write(fsys, path, status.Next(prev, c))))
}

// remeasureStatus rewrites the disk fields in state/status.json, if a sync
// wrote one Read can parse. Callers hold state/write.lock.
func remeasureStatus(fsys store.FS, roots config.Roots, cfg config.Config) error {
	path := filepath.Join(roots.State, "status.json")
	st, err := status.Read(path)
	if st == nil || err != nil {
		return nil
	}
	d, err := status.Measure(roots.Data, roots.State, repoKey(cfg))
	if err != nil {
		return err
	}
	return status.Write(fsys, path, status.Remeasured(*st, repoKey(cfg), d, time.Duration(cfg.Retention), int64(cfg.DiskCap)))
}

func repoKey(cfg config.Config) string { return cfg.Host + "/" + cfg.Repo }
