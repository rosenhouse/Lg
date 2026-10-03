package cli

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/rosenhouse/lg/internal/config"
)

type pathsCmd struct {
	Null bool `short:"0" help:"Separate paths with NUL instead of newline."`
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
	return filepath.WalkDir(roots.Data, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || d.Name() != "log.txt" {
			return err
		}
		_, err = fmt.Fprint(deps.Stdout, path, sep)
		return err
	})
}
