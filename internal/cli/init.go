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
	Repo string `required:"" placeholder:"OWNER/NAME" help:"Repository as owner/name."`
	Host string `default:"github.com" placeholder:"HOST" help:"GitHub host: github.com, or a GitHub Enterprise Server host."`
}

func (initCmd) Help() string {
	d := config.Defaults()
	return "Writes host and repo to $LG_CONFIG, default ${XDG_CONFIG_HOME:-~/.config}/lg/config.yaml, " +
		"and creates the store at $LG_HOME, default ${XDG_DATA_HOME:-~/.local/share}/lg. " +
		"It refuses to overwrite an existing config.yaml. " +
		fmt.Sprintf("Edit that file to set sync_interval (%s), backfill (%s), retention (%s), disk_cap (%s), artifact_max_bytes (%s), log_grace (%s) or api_url. ",
			d.SyncInterval, d.Backfill, d.Retention, d.DiskCap, d.ArtifactMaxBytes, d.LogGrace) +
		"lg reads its token from gh auth token --hostname HOST, so run gh auth login first."
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
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	_, err = fmt.Fprintf(deps.Stdout, "wrote %s; run `lg sync` next\n", file)
	return err
}
