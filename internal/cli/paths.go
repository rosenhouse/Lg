package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	if _, err := os.Stat(roots.Data); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	// The trailing separator makes WalkDir follow a symlinked data/.
	return filepath.WalkDir(roots.Data+string(filepath.Separator), func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || d.Name() != "log.txt" {
			return err
		}
		_, err = fmt.Fprint(deps.Stdout, path, sep)
		return err
	})
}
