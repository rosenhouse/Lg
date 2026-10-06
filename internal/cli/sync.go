package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/signal"
	"path/filepath"
	"syscall"
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

// cycleWait bounds how long sync --wait waits for the daemon's cycle.
const cycleWait = 15 * time.Minute

type syncCmd struct {
	Wait    bool           `help:"With a daemon running, wait for a cycle that starts after this request, and exit as that cycle did."`
	Timeout *time.Duration `help:"How long to wait for another lg writing the store (default ${write_lock_wait}), or with --wait for the daemon's cycle (default ${cycle_wait})."`
}

func (c syncCmd) Run(deps *Deps) error {
	t, err := loadTarget(deps.Env)
	if err != nil {
		return err
	}
	running, err := daemon.Running(t.roots.State)
	if err != nil {
		return failure.FromErrno(err)
	}
	if running {
		since, timeout := deps.Clock.Now(), c.timeout(cycleWait)
		n, err := daemon.Request(deps.StoreFS, t.roots.State, timeout, deps.Clock)
		if err != nil {
			return failure.FromErrno(err)
		}
		// Like the daemon's log, this line must not decide the outcome, so a
		// broken pipe gives EPIPE, which it ignores.
		signal.Ignore(syscall.SIGPIPE)
		_, _ = fmt.Fprintf(deps.Stderr, "lg: sent sync request %d to the running daemon\n", n)
		if !c.Wait {
			return nil
		}
		// The daemon reconciles lg.db only after it reports the cycle.
		return reconcileAfter(daemon.WaitForCycle(t.roots.State, n, since, timeout, deps.Clock), t.roots, deps)
	}
	held, err := lockWrites(context.Background(), t.roots, deps, c.timeout(writeLockWait))
	if err != nil {
		return failure.FromErrno(err)
	}
	// Ending ctx on a signal kills gh's process group, which the signal does not reach.
	ctx, stop := signalContext()
	defer stop()
	_, err = recordCycle(t, deps, func() (mirror.Report, error) { return runCycle(ctx, t, deps) }, func(*status.Cycle) {})
	stop()
	_ = held.Release()
	return reconcileAfter(err, t.roots, deps)
}

// reconcileAfter reconciles lg.db after a cycle that succeeded, and gives
// the cycle's error. Like the daemon, it only warns when reconcile fails.
func reconcileAfter(cycleErr error, roots config.Roots, deps *Deps) error {
	if cycleErr != nil {
		return cycleErr
	}
	if err := reconcileIndex(context.Background(), roots); err != nil {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: warning: reconcile lg.db: %s\n", status.OneLine(err.Error()))
	}
	return nil
}

func (c syncCmd) Validate() error { return validateTimeout(c.timeout(0)) }

func (c syncCmd) timeout(fallback time.Duration) time.Duration {
	if c.Timeout == nil {
		return fallback
	}
	return *c.Timeout
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

// recordCycle runs cycle and writes status.json, with the fields that
// complete adds. Callers hold state/write.lock, or know no writer can.
func recordCycle(t target, deps *Deps, cycle func() (mirror.Report, error), complete func(*status.Cycle)) (status.Cycle, error) {
	c := status.Cycle{Started: deps.Clock.Now()}
	report, err := cycle()
	c.Finished, c.Err, c.Completed = deps.Clock.Now(), err, report.Completed
	c.Pending, c.DefaultBranch = pending(report.Pending), report.DefaultBranch
	complete(&c)
	return c, errors.Join(err, writeStatus(deps.StoreFS, t.roots, t.cfg, c))
}

// runCycle opens the store and runs one cycle. Callers hold state/write.lock.
func runCycle(ctx context.Context, t target, deps *Deps) (mirror.Report, error) {
	cfg := t.cfg
	s, err := initAndSweep(deps.StoreFS, t.roots.Store)
	if err != nil {
		return mirror.Report{}, failure.FromErrno(err)
	}
	m := mirror.Mirror{
		Tokens: auth.GhTokenSource{Runner: deps.Runner, Env: deps.Env},
		NewGitHub: func(token string) github.Client {
			return deps.NewGitHub(t.api, cfg.Repo, token, deps.Clock)
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
