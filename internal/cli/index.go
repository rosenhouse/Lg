package cli

import (
	"context"
	"errors"
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
	if err := os.MkdirAll(roots.State, 0o755); err != nil {
		return err
	}
	ix, err := index.Open(filepath.Join(roots.State, "lg.db"), roots.Data)
	if err != nil {
		return err
	}
	return errors.Join(ix.Rebuild(context.Background()), ix.Close())
}
