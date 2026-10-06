package cli

import (
	"context"
	"io"

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

// NewLineReader gives a LineReader that opens files with open.
func NewLineReader(open func(path string) (io.ReadSeekCloser, error)) *LineReader {
	return newLineReader(open)
}

func (l *LineReader) Line(path string, n int) (string, bool) { return l.line(path, n) }

func (l *LineReader) Close() { l.close() }
