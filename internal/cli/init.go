package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/failure"
)

type initCmd struct {
	Repo string `required:"" help:"Repository as owner/name."`
	Host string `default:"github.com" help:"GitHub host."`
}

// Run writes config.yaml, holding only host and repo, and initializes the
// store. It creates the file before the store, so a refused or failed config
// leaves no store behind, and removes the file when the store fails.
func (c initCmd) Run(deps *Deps) error {
	cfg := config.Defaults()
	cfg.Host, cfg.Repo = strings.ToLower(c.Host), c.Repo
	if err := config.Validate(cfg); err != nil {
		return err
	}
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	file, err := config.File(deps.Env)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return config.Error(file + " already exists")
	}
	if err != nil {
		return err
	}
	_, release, err := openForWriting(context.Background(), roots, deps, writeLockWait, nil)
	if err != nil {
		return errors.Join(failure.FromErrno(err), f.Close(), os.Remove(file))
	}
	release()
	_, err = fmt.Fprintf(f, "host: %s\nrepo: %s\n", cfg.Host, cfg.Repo)
	return errors.Join(err, f.Close())
}
