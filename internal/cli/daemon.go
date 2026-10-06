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

	"github.com/rosenhouse/lg/internal/daemon"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/status"
	"github.com/rosenhouse/lg/internal/version"
)

type daemonCmd struct {
	Run daemonRunCmd `cmd:"" help:"Sync every sync_interval, and at each lg sync, until SIGTERM."`
}

type daemonRunCmd struct{}

func (daemonRunCmd) Run(deps *Deps) error {
	// Handling signals before locking lets a signal at any point end the daemon cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	t, err := loadTarget(deps.Env)
	if err != nil {
		return err
	}
	state := t.roots.State
	if err := os.MkdirAll(state, 0o755); err != nil {
		return failure.FromErrno(err)
	}
	instance, err := daemon.LockInstance(state, deps.Clock)
	if err != nil {
		return err
	}
	defer func() { _ = instance.Release() }()
	d := daemonCycle{deps: deps, target: t}
	loop := daemon.Loop{
		Clock:     deps.Clock,
		Cycle:     d.run,
		Requested: func() (int64, error) { return daemon.Requested(state) },
		Reconcile: func(ctx context.Context) error {
			ix, err := index.Open(ctx, filepath.Join(state, "lg.db"), t.roots.Data)
			if err != nil {
				return err
			}
			return errors.Join(ix.Reconcile(ctx), ix.Close())
		},
		Log: deps.Stderr,
	}
	if st, _ := status.Read(filepath.Join(state, "status.json")); st != nil && st.Blocked != nil && st.Blocked.RetryAt != nil {
		loop.RetryAt = *st.Blocked.RetryAt
	}
	loop.Run(ctx)
	return nil
}

// daemonCycle runs a daemon's cycles with the last good config.yaml.
type daemonCycle struct {
	deps   *Deps
	target target
}

// run reads config.yaml after it takes write.lock, so it syncs with an edit
// made while it waited.
func (d *daemonCycle) run(ctx context.Context, served int64) daemon.Outcome {
	held, err := lockWrites(ctx, d.target.roots, d.deps, writeLockWait)
	if err != nil {
		now := d.deps.Clock.Now()
		return daemon.Outcome{Started: now, Next: daemon.Next(now, time.Duration(d.target.cfg.SyncInterval), time.Time{}), Err: err}
	}
	defer func() { _ = held.Release() }()
	fresh, configErr := loadTarget(d.deps.Env)
	if configErr == nil {
		d.target = fresh
	} else {
		configErr = fmt.Errorf("%w; kept the last good config", configErr)
	}
	c, err := recordCycle(ctx, d.target, d.deps, func(c *status.Cycle) {
		c.Daemon = &status.Daemon{PID: os.Getpid(), Version: version.Version}
		c.NextSyncAt = daemon.Next(c.Started, time.Duration(d.target.cfg.SyncInterval), retryAt(c.Err))
		c.ServedRequest = served
		c.ConfigError = configErr
	})
	return daemon.Outcome{Started: c.Started, Next: c.NextSyncAt, RetryAt: retryAt(c.Err), Err: errors.Join(configErr, err)}
}

// retryAt gives the time a Blocked err defers the next cycle to, or zero.
func retryAt(err error) time.Time {
	var blocked failure.Blocked
	if errors.As(err, &blocked) {
		return blocked.RetryAt
	}
	return time.Time{}
}
