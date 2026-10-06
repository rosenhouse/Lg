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
	JSON bool `name:"json" help:"Print state/status.json as it is."`
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
	blocked := "no"
	if st.Blocked != nil {
		blocked = st.Blocked.String()
	}
	lines := []string{
		fmt.Sprintf("last sync: %s, finished %s (cycle %d)", st.LastSyncStartedAt.Format(time.RFC3339), st.LastSyncFinishedAt.Format(time.RFC3339), st.Cycle),
		"last ok sync: " + orNone(st.LastSyncOKAt, "never"),
		"next sync: " + orNone(st.NextSyncAt, "none scheduled"),
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
			fmt.Sprintf("  newest completed run: %s, lag: %s", orNone(r.NewestCompletedRunCreatedAt, "none"), lag),
			fmt.Sprintf("  runs: %d, attempts: %d, bytes: %d", r.Runs, r.Attempts, r.BytesData),
			fmt.Sprintf("  pending units: %d", r.PendingUnits))
		for _, pending := range r.Pending {
			lines = append(lines, "    "+pending.String())
		}
		lines = append(lines,
			"  horizon: "+orNone(r.Horizon, "none"),
			fmt.Sprintf("  retention: %d days, disk_cap: %d bytes", r.RetentionDays, r.DiskCapBytes))
	}
	return lines
}

func orNone(t *time.Time, none string) string {
	if t == nil {
		return none
	}
	return t.Format(time.RFC3339)
}
