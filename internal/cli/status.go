package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/lock"
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
			return fmt.Errorf("%s does not exist; run `lg sync`", path)
		}
		if err != nil {
			return err
		}
		_, err = deps.Stdout.Write(raw)
		return err
	}
	st, err := status.Read(path)
	if err != nil {
		return err
	}
	daemon, err := lock.Held(filepath.Join(roots.State, "daemon.lock"))
	if err != nil {
		return err
	}
	return printStatus(deps.Stdout, st, daemon)
}

func printStatus(w io.Writer, st *status.Status, daemon bool) error {
	_, err := io.WriteString(w, strings.Join(statusLines(st, daemon), "\n")+"\n")
	return err
}

func statusLines(st *status.Status, daemon bool) []string {
	var lines []string
	if st == nil {
		lines = append(lines, "last sync: never")
	} else {
		blocked := "no"
		if st.Blocked != nil {
			blocked = st.Blocked.String()
		}
		lines = append(lines,
			fmt.Sprintf("last sync: %s, finished %s (cycle %d)", st.LastSyncStartedAt.Format(time.RFC3339), st.LastSyncFinishedAt.Format(time.RFC3339), st.Cycle),
			"last ok sync: "+orNone(st.LastSyncOKAt, "never"),
			"next sync: "+orNone(st.NextSyncAt, "none scheduled"),
			"blocked: "+blocked)
	}
	if daemon {
		lines = append(lines, "daemon: running")
	} else {
		lines = append(lines, "daemon: not running")
	}
	if st == nil {
		return lines
	}
	for name, r := range st.Repos {
		lag := "none"
		if r.LagSeconds != nil {
			lag = (time.Duration(*r.LagSeconds) * time.Second).String()
		}
		lines = append(lines,
			name+":",
			"  default branch: "+r.DefaultBranch,
			fmt.Sprintf("  newest completed run: %s, lag: %s", orNone(r.NewestCompletedRunCreatedAt, "none"), lag),
			fmt.Sprintf("  runs: %d, attempts: %d, bytes: %d", r.Runs, r.Attempts, r.BytesData),
			fmt.Sprintf("  pending units: %d", r.PendingUnits))
		for _, pending := range r.Pending {
			lines = append(lines, "    "+pending)
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
