package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

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
	// A closed stdout must not kill gc between evictions.
	signal.Ignore(syscall.SIGPIPE)
	roots, cfg, err := loadConfig(deps.Env)
	if err != nil {
		return err
	}
	// gc only removes, so a root without a store is a mistyped LG_HOME, unless
	// a writer holding state/write.lock is making the store.
	format := filepath.Join(roots.Store, "FORMAT")
	if !exists(format) && !exists(filepath.Join(roots.State, "write.lock")) {
		return noStore(roots)
	}
	now, keep, diskCap := deps.Clock.Now(), time.Duration(cfg.Retention), int64(cfg.DiskCap)
	if c.DryRun {
		h, err := retention.PeekHorizons(roots.State)
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
	s, release, err := openForWriting(roots, deps, c.Timeout, func() error {
		if !exists(format) {
			return noStore(roots)
		}
		return nil
	})
	if err != nil {
		return failure.FromErrno(err)
	}
	defer release()
	// A failed print is no reason to stop removing, nor a blocked store.
	var printErr error
	err = retention.Retain(context.Background(), s, now, keep, diskCap, func(dir string) {
		if printErr == nil {
			_, printErr = fmt.Fprintln(deps.Stdout, dir)
		}
	})
	err = errors.Join(err, remeasureStatus(deps.StoreFS, roots, cfg))
	return errors.Join(failure.FromErrno(err), printErr)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
}
