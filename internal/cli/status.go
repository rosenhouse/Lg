package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/daemon"
	"github.com/rosenhouse/lg/internal/status"
)

type statusCmd struct {
	JSON bool `name:"json" help:"Print $LG_HOME/state/status.json as it is. It fails before the first sync."`
}

func (statusCmd) Help() string {
	return "The lag is the time from the newest completed run's creation to the last sync's finish, so it grows with each sync that finds no newer run. " +
		"Every lg command warns on stderr while syncs are blocked. " +
		"It also warns while the last successful sync, or a pending unit, is older than twice sync_interval. " +
		"That sync_interval is the one the last sync used, not the one config.yaml now sets."
}

func (c statusCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	path := filepath.Join(roots.State, "status.json")
	if c.JSON {
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s does not exist", path)
		}
		if err != nil {
			return err
		}
		_, err = deps.Stdout.Write(raw)
		return err
	}
	st, err := status.Read(path)
	if err != nil {
		return warned{err}
	}
	running, err := daemon.Running(roots.State)
	if err != nil {
		return err
	}
	_, err = io.WriteString(deps.Stdout, strings.Join(statusLines(st, running), "\n")+"\n")
	return err
}

func statusLines(st *status.Status, daemon bool) []string {
	daemonLine := "daemon: not running"
	if daemon {
		daemonLine = "daemon: running"
	}
	if st == nil {
		return []string{"last sync: never", daemonLine}
	}
	next := orNone(st.NextSyncAt, "none scheduled")
	if !daemon {
		next = "none (daemon not running)"
	}
	blocked := "no"
	if st.Blocked != nil {
		blocked = st.Blocked.String()
	}
	lines := []string{
		fmt.Sprintf("last sync: %s, finished %s (cycle %d)", st.LastSyncStartedAt.Format(time.RFC3339), st.LastSyncFinishedAt.Format(time.RFC3339), st.Cycle),
		"last ok sync: " + orNone(st.LastSyncOKAt, "never"),
		"next sync: " + next,
		"blocked: " + blocked,
	}
	if st.ConfigError != nil {
		lines = append(lines, "config error: "+*st.ConfigError)
	}
	lines = append(lines, daemonLine)
	for _, name := range slices.Sorted(maps.Keys(st.Repos)) {
		r := st.Repos[name]
		lag := "none"
		if r.LagSeconds != nil {
			lag = (time.Duration(*r.LagSeconds) * time.Second).String()
		}
		lines = append(lines,
			name+":",
			"  default branch: "+cmp.Or(r.DefaultBranch, "unknown"),
			fmt.Sprintf("  newest completed run created: %s, lag: %s", orNone(r.NewestCompletedRunCreatedAt, "none"), lag),
			fmt.Sprintf("  runs: %d, attempts: %d, bytes: %d", r.Runs, r.Attempts, r.BytesData),
			fmt.Sprintf("  pending units: %d", r.PendingUnits))
		for _, pending := range r.Pending {
			lines = append(lines, "    "+pending.String())
		}
		lines = append(lines,
			"  horizon: "+orNone(r.Horizon, "none"),
			fmt.Sprintf("  retention: %d days, disk_cap: %s", r.RetentionDays, config.Bytes(r.DiskCapBytes)))
	}
	return lines
}

func orNone(t *time.Time, none string) string {
	if t == nil {
		return none
	}
	return t.Format(time.RFC3339)
}
