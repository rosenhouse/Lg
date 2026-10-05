package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/retention"
)

type gcCmd struct {
	DryRun  bool          `help:"Print the dirs gc would remove, and remove nothing."`
	Timeout time.Duration `default:"${write_lock_wait}" help:"How long to wait for another lg writing the store."`
}

// Run prints each dir it removes. A dry run takes no lock, so it reads the
// store as it is.
func (c gcCmd) Run(deps *Deps) error {
	roots, cfg, err := loadConfig(deps.Env)
	if err != nil {
		return err
	}
	// gc only removes, so a root without a store is a mistyped LG_HOME.
	if _, err := os.Lstat(filepath.Join(roots.Store, "FORMAT")); errors.Is(err, fs.ErrNotExist) {
		return config.Error(fmt.Sprintf("%s holds no lg store; check LG_HOME", roots.Store))
	}
	now, keep, diskCap := deps.Clock.Now(), time.Duration(cfg.Retention), int64(cfg.DiskCap)
	if c.DryRun {
		h, err := retention.PeekHorizon(roots.State)
		if err != nil {
			return err
		}
		v, err := retention.Find(roots.Data, h, now, keep, diskCap)
		for _, dir := range v.Dirs() {
			if _, err := fmt.Fprintln(deps.Stdout, dir); err != nil {
				return err
			}
		}
		return err
	}
	s, release, err := openForWriting(roots, deps, c.Timeout)
	if err != nil {
		return failure.FromErrno(err)
	}
	defer release()
	return failure.FromErrno(retention.Retain(context.Background(), s, now, keep, diskCap, deps.Stdout))
}
