package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/alecthomas/kong"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/index"
)

// pathsCmd's repeatable flags take commas literally, as names and globs may hold them.
type pathsCmd struct {
	Branch     []string `sep:"none" help:"Only runs on this branch, and not from a fork."`
	SHA        []string `sep:"none" name:"sha" help:"Only runs whose head SHA starts with this."`
	PR         []int    `sep:"none" name:"pr" help:"Only runs of this pull request. Runs from a fork list none."`
	Workflow   []string `sep:"none" help:"Only runs of this workflow name."`
	Job        []string `sep:"none" help:"Only jobs whose name matches this glob, and the attempts and runs holding one."`
	Event      []string `sep:"none" help:"Only runs triggered by this event."`
	Conclusion []string `sep:"none" help:"Only units that concluded so: the job, the attempt, or else the run's latest attempt."`
	Since      moment   `placeholder:"TIME" help:"Only units since this time: 30d, 12h, 2026-09-01 (UTC) or RFC 3339."`
	Until      moment   `placeholder:"TIME" help:"Only units until this time, inclusive. A date means its 00:00 UTC."`
	Unit       *string  `enum:"run,attempt,job,log,artifact,extracted" help:"Print the files of this unit (${enum}) instead of logs and extracted files."`
	Null       bool     `short:"0" help:"Separate paths with NUL instead of newline."`
}

func (pathsCmd) Help() string {
	return "A unit must match every flag given, and any value of a flag given more than once."
}

// Validate refuses an empty value, which a filter would take as matching every
// run or none.
func (p pathsCmd) Validate() error {
	flags := map[string][]string{
		"branch": p.Branch, "sha": p.SHA, "workflow": p.Workflow, "job": p.Job, "event": p.Event, "conclusion": p.Conclusion,
	}
	for _, flag := range slices.Sorted(maps.Keys(flags)) {
		if slices.Contains(flags[flag], "") {
			return fmt.Errorf("--%s must not be empty", flag)
		}
	}
	return nil
}

// moment is a --since or --until: a duration before now, or a time.
type moment struct {
	set bool
	ago time.Duration
	at  time.Time
}

func (m *moment) Decode(ctx *kong.DecodeContext) error {
	var s string
	if err := ctx.Scan.PopValueInto("time", &s); err != nil {
		return err
	}
	parsed, err := parseMoment(s)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

func parseMoment(s string) (moment, error) {
	if ago, err := config.ParseDuration(s); err == nil && ago >= 0 {
		return moment{set: true, ago: ago}, nil
	}
	for _, layout := range []string{time.DateOnly, time.RFC3339} {
		at, err := time.Parse(layout, s)
		if err != nil {
			continue
		}
		// The zero time.Time means a relative moment, and index.Filter's unset bound.
		if at.Before(time.Unix(0, 0)) {
			return moment{}, fmt.Errorf("want a time since 1970, not %q", s)
		}
		return moment{set: true, at: at}, nil
	}
	return moment{}, fmt.Errorf("want 30d, 12h, 2026-09-01 or an RFC 3339 time, not %q", s)
}

// time gives the moment as of now, or the zero time when it is not set.
func (m moment) time(now time.Time) time.Time {
	switch {
	case !m.set:
		return time.Time{}
	case m.at.IsZero():
		return now.Add(-m.ago)
	}
	return m.at
}

func (p pathsCmd) filter(now time.Time) index.Filter {
	return index.Filter{
		Branches: p.Branch, SHAs: p.SHA, PRs: p.PR, Workflows: p.Workflow, Jobs: p.Job, Events: p.Event,
		Conclusions: p.Conclusion, Since: p.Since.time(now), Until: p.Until.time(now),
	}
}

func (p pathsCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	sep := "\n"
	if p.Null {
		sep = "\x00"
	}
	if _, err := os.Lstat(roots.Data); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var unit index.Unit
	if p.Unit != nil {
		unit = index.Unit(*p.Unit)
	}
	ctx := context.Background()
	ix, err := openIndex(ctx, roots, deps)
	if err != nil {
		return err
	}
	defer func() { _ = ix.Close() }()
	// A run or unit lg cannot read leaves the others answerable.
	reconciled := ix.Reconcile(ctx)
	paths, unread := ix.Paths(ctx, p.filter(deps.Clock.Now()), unit)
	for _, path := range paths {
		if _, err := fmt.Fprint(deps.Stdout, path, sep); err != nil {
			return err
		}
	}
	return errors.Join(reconciled, unread)
}
