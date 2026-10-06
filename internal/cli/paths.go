package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/index"
)

type pathsCmd struct {
	filters `embed:""`
	Unit    []string `sep:"none" enum:"run,attempt,job,log,artifact,extracted" help:"Print the files of this unit (${enum}) instead of logs and extracted files."`
	Null    bool     `short:"0" help:"Separate paths with NUL instead of newline."`
}

func (pathsCmd) Help() string {
	return "A unit must match every flag given, and any value of a flag given more than once."
}

// Validate refuses more than one --unit.
func (p pathsCmd) Validate() error {
	if len(p.Unit) > 1 {
		return errors.New("--unit must not be given more than once")
	}
	return p.filters.validate()
}

func (p pathsCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(roots.Data); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var unit index.Unit
	if len(p.Unit) > 0 {
		unit = index.Unit(p.Unit[0])
	}
	ctx := context.Background()
	ix, reconciled := answerable(ctx, roots, deps)
	if ix == nil {
		return reconciled
	}
	defer func() { _ = ix.Close() }()
	paths, unread := ix.Paths(ctx, p.filter(deps.Clock.Now()), unit)
	unprintable, err := p.print(deps.Stdout, paths)
	if err != nil {
		return err
	}
	return errors.Join(reconciled, unread, unprintable)
}

// answerable gives lg.db, reconciled, or else an index of data/ in memory
// when lg.db is unusable, with the errors of the units it could not read.
func answerable(ctx context.Context, roots config.Roots, deps *Deps) (*index.Index, error) {
	ix, err := openIndex(ctx, roots, deps)
	if err == nil {
		// A run or unit lg cannot read leaves the others answerable.
		err = ix.Reconcile(ctx)
	}
	// A full or read-only disk leaves lg.db unusable, but data/ readable.
	var unusable *index.DBError
	if !errors.As(err, &unusable) {
		return ix, err
	}
	if ix != nil {
		_ = ix.Close()
	}
	_, _ = fmt.Fprintf(deps.Stderr, "lg: warning: indexing data/ in memory, since %v\n", unusable)
	memory, err := index.OpenMemory(ctx, roots.Data)
	if err != nil {
		return nil, err
	}
	return memory, memory.Reconcile(ctx)
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
