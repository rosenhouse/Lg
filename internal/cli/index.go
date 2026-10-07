package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/index"
)

type indexCmd struct {
	Rebuild indexRebuildCmd `cmd:"" help:"Rebuild state/lg.db from data/."`
}

type indexRebuildCmd struct{}

func (indexRebuildCmd) Run(deps *Deps) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	if !exists(filepath.Join(roots.Store, "FORMAT")) {
		return noStore(roots)
	}
	db := filepath.Join(roots.State, "lg.db")
	return index.Rebuild(context.Background(), db, roots.Data, waitingFor(deps, db+".lock"))
}

// query runs read on the index of data/, reconciled. Before the first sync
// creates data/, it reads nothing.
func query(deps *Deps, read func(context.Context, *index.Index, config.Roots) error) error {
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(roots.Data); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	ctx := context.Background()
	ix, reconciled := answerable(ctx, roots, deps)
	if ix == nil {
		return reconciled
	}
	defer func() { _ = ix.Close() }()
	return errors.Join(reconciled, read(ctx, ix, roots))
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
