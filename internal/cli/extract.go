package cli

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"github.com/rosenhouse/lg/internal/config"
	"github.com/rosenhouse/lg/internal/extract"
	"github.com/rosenhouse/lg/internal/failure"
	"github.com/rosenhouse/lg/internal/index"
	"github.com/rosenhouse/lg/internal/layout"
	"github.com/rosenhouse/lg/internal/retention"
	"github.com/rosenhouse/lg/internal/store"
)

type extractCmd struct {
	filters  `embed:""`
	All      bool          `help:"Extract every artifact."`
	MaxBytes size          `default:"${extract_max_bytes}" help:"Extract no artifact whose files, nested archives expanded, exceed this."`
	Timeout  time.Duration `default:"${write_lock_wait}" help:"How long to wait for another lg writing the store."`
	Paths    []string      `arg:"" optional:"" name:"path" help:"An artifact dir, or a file in one."`
}

func (extractCmd) Help() string {
	return "Expands each selected artifact.zip, and the zip, tar and tar.gz archives nested in it, into extracted/ beside it. " +
		"An artifact already extracted, or whose zip is a tombstone, is skipped. extracted/.lg-extract.json records what was renamed or skipped. " +
		fmt.Sprintf("An artifact.zip of more than %d members is not extracted, and nested archives past that many stay unexpanded.", extract.Defaults().MaxFiles)
}

// size is a --max-bytes.
type size config.Bytes

func (s *size) Decode(ctx *kong.DecodeContext) error {
	var v string
	if err := ctx.Scan.PopValueInto("size", &v); err != nil {
		return err
	}
	parsed, err := config.ParseBytes(v)
	*s = size(parsed)
	return err
}

func (c extractCmd) Validate() error {
	if err := c.validate(); err != nil {
		return err
	}
	filtered, named := !c.empty(), len(c.Paths) > 0
	switch {
	case c.All && (filtered || named):
		return errors.New("--all takes no filters or PATHs")
	case filtered && named:
		return errors.New("give filters or PATHs, not both")
	case !c.All && !filtered && !named:
		return errors.New("give filters, PATHs or --all")
	}
	return validateTimeout(c.Timeout)
}

// Run prints the extracted/ dir of each artifact it extracts.
func (c extractCmd) Run(deps *Deps) error {
	// A closed stdout must not stop extract between artifacts.
	signal.Ignore(syscall.SIGPIPE)
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	s, release, err := openExisting(roots, deps, c.Timeout)
	if err != nil {
		return failure.FromErrno(err)
	}
	defer release()
	dirs, selectErr := c.selected(deps, roots)
	limits := extract.Defaults()
	limits.MaxBytes = int64(c.MaxBytes)
	errs := []error{selectErr}
	var printErr error
	anyExtracted := false
	for _, dir := range dirs {
		extracted, err := c.extract(deps, s, dir, limits)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if extracted {
			anyExtracted = true
			if printErr == nil {
				_, printErr = fmt.Fprintln(deps.Stdout, filepath.Join(dir, "extracted"))
			}
		}
	}
	if anyExtracted {
		warnPastDiskCap(deps)
	}
	return errors.Join(append(errs, printErr)...)
}

// warnPastDiskCap says when retention would now evict extracted/ trees. It
// says nothing when it cannot tell.
func warnPastDiskCap(deps *Deps) {
	roots, cfg, err := loadConfig(deps.Env)
	if err != nil {
		return
	}
	h, err := retention.PeekHorizons(roots.State)
	if err != nil {
		return
	}
	v, err := retention.Find(roots.Data, h, deps.Clock.Now(), time.Duration(cfg.Retention), int64(cfg.DiskCap))
	if err == nil && len(v.Extracted) > 0 {
		_, _ = fmt.Fprintln(deps.Stderr, "lg: data/ now exceeds disk_cap; the next cycle evicts extracted/ trees, oldest run first")
	}
}

// extract extracts the artifact in dir, unless it has been, or its zip is a
// tombstone, and reports whether it did.
func (c extractCmd) extract(deps *Deps, s *store.Store, dir string, limits extract.Limits) (bool, error) {
	switch {
	case exists(filepath.Join(dir, "extracted")):
		return false, nil
	case exists(filepath.Join(dir, "artifact.zip.tombstone")):
		_, _ = fmt.Fprintf(deps.Stderr, "lg: skipped %s, whose artifact.zip is a tombstone\n", dir)
		return false, nil
	}
	err := extract.Extract(s, dir, limits, deps.Clock.Now())
	if errors.Is(err, store.ErrTooLarge) {
		return false, fmt.Errorf("%s: extracted nothing, since its files exceed --max-bytes %d", dir, limits.MaxBytes)
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", dir, err)
	}
	return true, nil
}

// selected gives the artifact dirs that the PATHs name, or else those of the
// artifacts that the filters, or --all, select.
func (c extractCmd) selected(deps *Deps, roots config.Roots) ([]string, error) {
	if len(c.Paths) > 0 {
		return artifactDirs(roots.Data, c.Paths)
	}
	var dirs []string
	err := query(deps, func(ctx context.Context, ix *index.Index, _ config.Roots) error {
		zips, err := ix.Paths(ctx, c.filter(deps.Clock.Now()), index.UnitArtifact)
		for _, zip := range zips {
			dirs = append(dirs, filepath.Dir(zip))
		}
		return err
	})
	return dirs, err
}

// artifactDirs gives the artifact dir under data holding each path.
func artifactDirs(data string, paths []string) ([]string, error) {
	realData, err := filepath.EvalSymlinks(data)
	if err != nil {
		return nil, err
	}
	var dirs []string
	var errs []error
	for _, path := range paths {
		rel, err := relToData(realData, data, path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		loc, err := layout.Parse(rel)
		switch {
		case err != nil:
			err = fmt.Errorf("%s: %w", path, err)
		case loc.ArtifactDir == "":
			err = fmt.Errorf("%s is not in an artifact dir", path)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		dirs = append(dirs, filepath.Join(data, loc.ArtifactDir))
	}
	return dirs, errors.Join(errs...)
}
