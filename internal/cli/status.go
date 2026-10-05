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

func printStatus(w io.Writer, st *status.Status, daemon bool) error {
	p := &printer{w: w}
	if st == nil {
		p.line("last sync: never")
	} else {
		p.line("last sync: %s, finished %s (cycle %d)", st.LastSyncStartedAt.Format(time.RFC3339), st.LastSyncFinishedAt.Format(time.RFC3339), st.Cycle)
		p.line("last ok sync: %s", orNone(st.LastSyncOKAt, "never"))
		p.line("next sync: %s", orNone(st.NextSyncAt, "none scheduled"))
		blocked := "no"
		if b := st.Blocked; b != nil {
			blocked = fmt.Sprintf("%s since %s", b.Kind, b.Since.Format(time.RFC3339))
			if b.RetryAt != nil {
				blocked += ", retry_at " + b.RetryAt.Format(time.RFC3339)
			}
			blocked += ": " + b.Detail
		}
		p.line("blocked: %s", blocked)
	}
	if daemon {
		p.line("daemon: running")
	} else {
		p.line("daemon: not running")
	}
	if st == nil {
		return p.err
	}
	for name, r := range st.Repos {
		p.line("%s:", name)
		p.line("  default branch: %s", r.DefaultBranch)
		lag := "none"
		if r.LagSeconds != nil {
			lag = (time.Duration(*r.LagSeconds) * time.Second).String()
		}
		p.line("  newest completed run: %s, lag: %s", orNone(r.NewestCompletedRunCreatedAt, "none"), lag)
		p.line("  runs: %d, attempts: %d, bytes: %d", r.Runs, r.Attempts, r.BytesData)
		p.line("  pending units: %d", r.PendingUnits)
		for _, pending := range r.Pending {
			p.line("    %s", pending)
		}
		p.line("  horizon: %s", orNone(r.Horizon, "none"))
		p.line("  retention: %d days, disk_cap: %d bytes", r.RetentionDays, r.DiskCapBytes)
	}
	return p.err
}

// printer writes lines until its first error.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) line(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format+"\n", args...)
	}
}

func orNone(t *time.Time, none string) string {
	if t == nil {
		return none
	}
	return t.Format(time.RFC3339)
}
