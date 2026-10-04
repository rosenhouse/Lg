package cli

import (
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

// Run writes config.yaml, holding only host and repo, and initializes the store.
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
	if _, err := os.Lstat(file); err == nil {
		return config.Error(file + " already exists")
	}
	_, release, err := openForWriting(roots, deps)
	if err != nil {
		return failure.FromErrno(err)
	}
	release()
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
	_, err = fmt.Fprintf(f, "host: %s\nrepo: %s\n", cfg.Host, cfg.Repo)
	return errors.Join(err, f.Close())
}
