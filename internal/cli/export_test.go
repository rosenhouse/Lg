package cli

import (
	"context"
	"io"
	"io/fs"

	"github.com/rosenhouse/lg/internal/daemon"
)

// RunDaemonCycle runs one cycle as a daemon started with deps would.
func RunDaemonCycle(ctx context.Context, deps Deps, serving func() int64) (daemon.Outcome, error) {
	t, err := loadTarget(deps.Env)
	if err != nil {
		return daemon.Outcome{}, err
	}
	d := daemonCycle{deps: &deps, target: t}
	return d.run(ctx, serving), nil
}

type LineReader = lineReader

func NewLineReader(open func(path string) (io.ReadSeekCloser, error)) *LineReader {
	return newLineReader(open)
}

func (l *LineReader) Line(path string, n int) (string, bool) { return l.line(path, n) }

func (l *LineReader) Close() { l.close() }

type DirCache = dirCache

func NewDirCache(readDir func(dir string) ([]fs.DirEntry, error)) *DirCache {
	return newDirCache(readDir)
}

func (c *DirCache) Stat(path string) (isDir, exists bool) { return c.stat(path) }

// FindPlaces decodes each hit in the store at data, listing dirs with readDir.
func FindPlaces(data string, readDir func(dir string) ([]fs.DirEntry, error), hits ...string) error {
	f := newPlaceFinder(data)
	defer f.close()
	f.dirs = newDirCache(readDir)
	for _, hit := range hits {
		if _, err := f.find(hit); err != nil {
			return err
		}
	}
	return nil
}

var PrintFlipJSON = printFlipJSON

var PrintIntermittentJSON = printIntermittentJSON

// Parse parses args as Main does, without running the command. --help
// parses as success.
func Parse(args []string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if r != kongExit(0) {
				panic(r)
			}
			err = nil
		}
	}()
	_, err = newParser(io.Discard, io.Discard).Parse(args)
	return err
}

var PrintFlip = printFlip

type FlipJSON = flipJSON

type IntermittentJSON = intermittentJSON
