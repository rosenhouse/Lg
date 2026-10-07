package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rosenhouse/lg/internal/index"
)

type pathsCmd struct {
	filters `embed:""`
	Unit    []string `sep:"none" enum:"run,attempt,job,log,artifact,extracted" help:"Print the files of this unit (${enum}) instead of logs and extracted files."`
	Null    bool     `short:"0" help:"Separate paths with NUL instead of newline."`
}

func (pathsCmd) Help() string {
	return "A unit must match every flag given, and any value of a flag given more than once. " +
		"--job also selects the attempts holding a matching job, and the runs, artifacts and extracted files of runs holding one. " +
		"--conclusion is of the job, the attempt, or else the run's latest attempt. --since and --until bound each unit's time."
}

// Validate refuses more than one --unit.
func (p pathsCmd) Validate() error {
	if len(p.Unit) > 1 {
		return errors.New("--unit must not be given more than once")
	}
	return p.validate()
}

func (p pathsCmd) Run(deps *Deps) error {
	var unit index.Unit
	if len(p.Unit) > 0 {
		unit = index.Unit(p.Unit[0])
	}
	return query(deps, func(ctx context.Context, ix *index.Index) error {
		paths, unread := ix.Paths(ctx, p.filter(deps.Clock.Now()), unit)
		unprintable, err := p.print(deps.Stdout, paths)
		if err != nil {
			return err
		}
		return errors.Join(unread, unprintable)
	})
}

// print writes the paths to w, each followed by the separator. Without -0,
// it gives the error of each path holding a newline instead of writing it.
func (p pathsCmd) print(w io.Writer, paths []string) (unprintable, err error) {
	sep := "\n"
	if p.Null {
		sep = "\x00"
	}
	var skipped []error
	for _, path := range paths {
		if !p.Null && strings.Contains(path, "\n") {
			skipped = append(skipped, fmt.Errorf("%q holds a newline; use -0", path))
			continue
		}
		if _, err := fmt.Fprint(w, path, sep); err != nil {
			return nil, err
		}
	}
	return errors.Join(skipped...), nil
}
