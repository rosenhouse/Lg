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
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/retention"
	"github.com/rosenhouse/lg/internal/store"
)

type extractCmd struct {
	filters  `embed:""`
	All      bool          `help:"Extract every artifact."`
	MaxBytes size          `default:"${extract_max_bytes}" help:"Extract no artifact whose files, nested archives expanded, exceed this size, such as 500MB or 2GB."`
	Timeout  time.Duration `default:"${write_lock_wait}" help:"How long to wait for another lg writing the store."`
	Paths    []string      `arg:"" optional:"" name:"path" help:"An artifact dir, or a file in one, relative to the working directory."`
}

func (extractCmd) Help() string {
	return "Give filters, PATHs or --all. Filters select artifacts as lg paths --unit artifact does. " +
		"Nested zip, tar and tar.gz archives expand beside themselves as <archive>.d/. " +
		"lg extract prints each extracted/ dir it writes, and says on stderr when it has nothing to extract. " +
		"It skips an artifact already extracted, or whose zip is a tombstone. " +
		fmt.Sprintf("An artifact.zip of more than %d members is not extracted, and nested archives past that many members stay unexpanded. ", extract.Defaults().MaxFiles) +
		"extracted/.lg-extract.json records each renamed or skipped member."
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
	// Ending ctx on a signal removes the artifact's staging from tmp/.
	ctx, stop := signalContext()
	defer stop()
	roots, err := config.Locations(deps.Env)
	if err != nil {
		return err
	}
	w := &writeTurns{ctx: ctx, deps: deps, roots: roots, timeout: c.Timeout}
	if err := w.take(); err != nil {
		return failure.FromErrno(err)
	}
	defer w.done()
	dirs, selectErr := c.selected(deps, roots)
	limits := extract.Defaults()
	limits.MaxBytes = int64(c.MaxBytes)
	errs := []error{selectErr}
	var printErr error
	anyExtracted := false
	for _, dir := range dirs {
		if err := w.next(); err != nil {
			errs = append(errs, failure.FromErrno(err))
			break
		}
		extracted, err := c.extract(ctx, deps, w.store, dir, limits)
		if err != nil {
			errs = append(errs, err)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if extracted {
			anyExtracted = true
			if printErr == nil {
				_, printErr = fmt.Fprintln(deps.Stdout, filepath.Join(dir, "extracted"))
			}
		}
	}
	w.done()
	err = errors.Join(append(errs, printErr)...)
	switch {
	case anyExtracted:
		warnPastDiskCap(deps)
	case err != nil:
	case len(dirs) == 0:
		_, _ = fmt.Fprintln(deps.Stderr, "lg: nothing to extract: no artifact matches")
	default:
		_, _ = fmt.Fprintln(deps.Stderr, "lg: nothing to extract: every artifact selected is extracted already, or its zip is a tombstone")
	}
	return err
}

// lg extract lets go of state/write.lock between artifacts once it has held it
// for writeTurn, for long enough that a writer waiting for it, such as a
// cycle, takes it.
const (
	writeTurn  = time.Second
	writeYield = 2 * lock.PollInterval
)

// writeTurns holds state/write.lock in turns.
type writeTurns struct {
	ctx     context.Context
	deps    *Deps
	roots   config.Roots
	timeout time.Duration
	store   *store.Store
	release func()
	since   time.Time
}

func (w *writeTurns) take() error {
	var err error
	w.store, w.release, err = openExisting(w.ctx, w.roots, w.deps, w.timeout)
	w.since = w.deps.Clock.Now()
	return err
}

// next lets go of the lock and takes it again, once this turn has lasted writeTurn.
func (w *writeTurns) next() error {
	if w.deps.Clock.Now().Sub(w.since) < writeTurn {
		return nil
	}
	w.done()
	select {
	case <-w.ctx.Done():
		return w.ctx.Err()
	case <-w.deps.Clock.After(writeYield):
	}
	return w.take()
}

func (w *writeTurns) done() {
	if w.release != nil {
		w.release()
		w.release = nil
	}
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
func (c extractCmd) extract(ctx context.Context, deps *Deps, s *store.Store, dir string, limits extract.Limits) (bool, error) {
	switch {
	case exists(filepath.Join(dir, "extracted")):
		return false, nil
	case exists(filepath.Join(dir, "artifact.zip.tombstone")):
		_, _ = fmt.Fprintf(deps.Stderr, "lg: skipped %s, whose artifact.zip is a tombstone\n", dir)
		return false, nil
	}
	err := extract.Extract(ctx, s, dir, limits, deps.Clock.Now())
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
