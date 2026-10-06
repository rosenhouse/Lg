package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/daemon"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/status"
	"github.com/rosenhouse/lg/internal/version"
)

type daemonCmd struct {
	Run       daemonRunCmd       `cmd:"" help:"Sync every sync_interval, and at each lg sync, until SIGTERM."`
	Install   daemonInstallCmd   `cmd:"" help:"Run lg daemon run as a per-user systemd unit or launchd agent, restarting it if installed."`
	Uninstall daemonUninstallCmd `cmd:"" help:"Stop and remove the service that lg daemon install set up."`
}

type daemonRunCmd struct{}

func (daemonRunCmd) Run(deps *Deps) error {
	// Handling signals before locking lets a signal at any point end the daemon cleanly.
	ctx, stop := signalContext()
	defer stop()
	// A closed stderr, as when a journal's stream breaks, then gives EPIPE, which logging ignores.
	signal.Ignore(syscall.SIGPIPE)
	t, err := loadTarget(deps.Env)
	if err != nil {
		return err
	}
	state := t.roots.State
	if err := os.MkdirAll(state, 0o755); err != nil {
		return failure.FromErrno(err)
	}
	instance, err := daemon.LockInstance(deps.StoreFS, state, deps.Clock, func(err error) {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: %s\n", err)
	})
	if err != nil {
		return err
	}
	defer func() { _ = instance.Release() }()
	// A cycle inits and sweeps too, so a failure here only waits for it.
	if err := initOnStart(ctx, t.roots, deps); err != nil && ctx.Err() == nil {
		_, _ = fmt.Fprintf(deps.Stderr, "lg: %s\n", err)
	}
	d := daemonCycle{deps: deps, target: t}
	loop := daemon.Loop{
		Clock:     deps.Clock,
		Cycle:     d.run,
		Requested: func() (int64, error) { return daemon.Requested(state) },
		Lost:      instance.Lost,
		Reconcile: func(ctx context.Context) error { return reconcileIndex(ctx, t.roots) },
		Log:       deps.Stderr,
	}
	if st, _ := status.Read(filepath.Join(state, "status.json")); st != nil && st.Blocked != nil && st.Blocked.RetryAt != nil {
		now := deps.Clock.Now()
		if until := failure.Capped(*st.Blocked.RetryAt, now); until.After(now) {
			loop.RetryAt = until
			_, _ = fmt.Fprintf(deps.Stderr, "lg: blocked until %s; first sync then\n", until.UTC().Format(time.RFC3339))
		}
	}
	return loop.Run(ctx)
}

// reconcileIndex brings state/lg.db up to date with data/.
func reconcileIndex(ctx context.Context, roots config.Roots) error {
	ix, err := index.Open(ctx, filepath.Join(roots.State, "lg.db"), roots.Data)
	if err != nil {
		return err
	}
	return errors.Join(ix.Reconcile(ctx), ix.Close())
}

// initOnStart initializes the store and sweeps what dead writers left in
// tmp/, even when a recorded retry_at defers the first cycle.
func initOnStart(ctx context.Context, roots config.Roots, deps *Deps) error {
	held, err := lockWrites(ctx, roots, deps, writeLockWait)
	if err != nil {
		return failure.FromErrno(err)
	}
	_, err = initAndSweep(deps.StoreFS, roots.Store)
	return failure.FromErrno(errors.Join(err, held.Release()))
}

// daemonCycle runs a daemon's cycles with the last good config.yaml.
type daemonCycle struct {
	deps   *Deps
	target target
}

// run reads config.yaml after it takes write.lock, so it syncs with an edit
// made while it waited. It skips the cycle when that wait times out, so the
// daemon retries a request. It records another lock error as the cycle, so
// sync --wait sees it and the daemon does not retry it every second. No
// writer can hold a lock that cannot be taken, so it writes status.json
// without one.
func (d *daemonCycle) run(ctx context.Context, serving func() int64) daemon.Outcome {
	held, err := lockWrites(ctx, d.target.roots, d.deps, writeLockWait)
	if errors.Is(err, lock.ErrTimeout) || ctx.Err() != nil {
		return daemon.Outcome{Started: d.deps.Clock.Now(), Interval: time.Duration(d.target.cfg.SyncInterval), Err: err, Skipped: true}
	}
	cycle := func() (mirror.Report, error) { return runCycle(ctx, d.target, d.deps) }
	var configErr error
	if err != nil {
		lockErr := failure.FromErrno(err)
		cycle = func() (mirror.Report, error) { return mirror.Report{}, lockErr }
	} else {
		defer func() { _ = held.Release() }()
		configErr = d.reloadConfig()
	}
	served := serving()
	var out daemon.Outcome
	_, err = recordCycle(d.target, d.deps, cycle, func(c *status.Cycle) {
		out = daemon.Outcome{Started: c.Started, Interval: time.Duration(d.target.cfg.SyncInterval), RetryAt: retryAt(c.Err)}
		c.Daemon = &status.Daemon{PID: os.Getpid(), Version: version.Version}
		c.NextSyncAt = out.Next()
		c.ServedRequest = served
		c.ConfigError = configErr
	})
	out.Err = errors.Join(configErr, err)
	return out
}

// reloadConfig reads config.yaml, keeping the last good one when it is not valid.
func (d *daemonCycle) reloadConfig() error {
	fresh, err := loadTarget(d.deps.Env)
	if err != nil {
		return fmt.Errorf("%w; kept the last good config", err)
	}
	d.target = fresh
	return nil
}

// retryAt gives the time a Blocked err defers the next cycle to, or zero.
func retryAt(err error) time.Time {
	var blocked failure.Blocked
	if errors.As(err, &blocked) {
		return blocked.RetryAt
	}
	return time.Time{}
}

// signalContext is done on SIGINT, SIGTERM, or SIGHUP unless SIGHUP is
// ignored, as nohup does.
func signalContext() (context.Context, context.CancelFunc) {
	signals := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if !signal.Ignored(syscall.SIGHUP) {
		signals = append(signals, syscall.SIGHUP)
	}
	return signal.NotifyContext(context.Background(), signals...)
}
