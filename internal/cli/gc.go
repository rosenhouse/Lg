package cli

import (
	"fmt"
	"time"

	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/retention"
)

type gcCmd struct {
	DryRun  bool          `help:"Print the dirs gc would remove, and remove nothing."`
	Timeout time.Duration `default:"5m" help:"How long to wait for another lg writing the store."`
}

// Run prints each dir it removes. A dry run takes no lock, so it reads the
// store as it is.
func (c gcCmd) Run(deps *Deps) error {
	roots, cfg, err := loadConfig(deps.Env)
	if err != nil {
		return err
	}
	find := func() (retention.Victims, error) {
		return retention.Find(roots.Data, deps.Clock.Now(), time.Duration(cfg.Retention), int64(cfg.DiskCap))
	}
	if c.DryRun {
		v, err := find()
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
	v, err := find()
	if err == nil {
		err = retention.Execute(s, v, deps.Stdout)
	}
	return failure.FromErrno(err)
}
