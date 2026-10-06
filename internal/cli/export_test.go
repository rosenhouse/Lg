package cli

import (
	"context"

	"github.com/rosenhouse/lg/internal/daemon"
)

// RunDaemonCycle runs one cycle as a daemon started with deps would.
func RunDaemonCycle(ctx context.Context, deps Deps, served int64) (daemon.Outcome, error) {
	t, err := loadTarget(deps.Env)
	if err != nil {
		return daemon.Outcome{}, err
	}
	d := daemonCycle{deps: &deps, target: t}
	return d.run(ctx, served), nil
}
