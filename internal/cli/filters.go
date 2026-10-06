package cli

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/alecthomas/kong"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/index"
)

// filters' repeatable flags take commas literally, as names and globs may hold them.
type filters struct {
	Branch     []string `sep:"none" help:"Only runs on this branch, and not from a fork."`
	SHA        []string `sep:"none" name:"sha" help:"Only runs whose head SHA starts with this."`
	PR         []int    `sep:"none" name:"pr" help:"Only runs of this pull request. Runs from a fork list none."`
	Workflow   []string `sep:"none" help:"Only runs of the workflow that has, or had, this name."`
	Job        []string `sep:"none" help:"Only jobs whose name matches this glob, the attempts holding one, and the runs, artifacts and extracted files of runs holding one."`
	Event      []string `sep:"none" help:"Only runs triggered by this event."`
	Conclusion []string `sep:"none" help:"Only units that concluded so: the job, the attempt, or else the run's latest attempt."`
	Since      moment   `placeholder:"TIME" help:"Only units since this time: 30d, 12h, 2026-09-01 (UTC) or RFC 3339."`
	Until      moment   `placeholder:"TIME" help:"Only units until this time, inclusive. A date means its 00:00 UTC."`
}

// validate refuses an empty value, which a filter would take as matching every
// run or none.
func (p filters) validate() error {
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
	if m.set {
		return errors.New("must not be given more than once")
	}
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

// resolve gives the moment as of now, or the zero time when it is not set.
// A duration counts back from the whole second, as unit times are whole seconds.
func (m moment) resolve(now time.Time) time.Time {
	switch {
	case !m.set:
		return time.Time{}
	case m.at.IsZero():
		return now.Truncate(time.Second).Add(-m.ago)
	}
	return m.at
}

func (p filters) filter(now time.Time) index.Filter {
	return index.Filter{
		Branches: p.Branch, SHAs: p.SHA, PRs: p.PR, Workflows: p.Workflow, Jobs: p.Job, Events: p.Event,
		Conclusions: p.Conclusion, Since: p.Since.resolve(now), Until: p.Until.resolve(now),
	}
}
