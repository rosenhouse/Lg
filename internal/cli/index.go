package cli

import (
	"context"
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
