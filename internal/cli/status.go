package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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

func printStatus(stdout io.Writer, st *status.Status, daemon bool) error {
	w := &errWriter{w: stdout}
	if st == nil {
		fmt.Fprintln(w, "last sync: never")
	} else {
		fmt.Fprintf(w, "last sync: %s, finished %s (cycle %d)\n", st.LastSyncStartedAt.Format(time.RFC3339), st.LastSyncFinishedAt.Format(time.RFC3339), st.Cycle)
		fmt.Fprintf(w, "last ok sync: %s\n", orNone(st.LastSyncOKAt, "never"))
		fmt.Fprintf(w, "next sync: %s\n", orNone(st.NextSyncAt, "none scheduled"))
		blocked := "no"
		if st.Blocked != nil {
			blocked = st.Blocked.String()
		}
		fmt.Fprintf(w, "blocked: %s\n", blocked)
	}
	if daemon {
		fmt.Fprintln(w, "daemon: running")
	} else {
		fmt.Fprintln(w, "daemon: not running")
	}
	if st == nil {
		return w.err
	}
	for name, r := range st.Repos {
		fmt.Fprintf(w, "%s:\n", name)
		fmt.Fprintf(w, "  default branch: %s\n", r.DefaultBranch)
		lag := "none"
		if r.LagSeconds != nil {
			lag = (time.Duration(*r.LagSeconds) * time.Second).String()
		}
		fmt.Fprintf(w, "  newest completed run: %s, lag: %s\n", orNone(r.NewestCompletedRunCreatedAt, "none"), lag)
		fmt.Fprintf(w, "  runs: %d, attempts: %d, bytes: %d\n", r.Runs, r.Attempts, r.BytesData)
		fmt.Fprintf(w, "  pending units: %d\n", r.PendingUnits)
		for _, pending := range r.Pending {
			fmt.Fprintf(w, "    %s\n", pending)
		}
		fmt.Fprintf(w, "  horizon: %s\n", orNone(r.Horizon, "none"))
		fmt.Fprintf(w, "  retention: %d days, disk_cap: %d bytes\n", r.RetentionDays, r.DiskCapBytes)
	}
	return w.err
}

func orNone(t *time.Time, none string) string {
	if t == nil {
		return none
	}
	return t.Format(time.RFC3339)
}
