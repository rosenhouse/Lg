// Package recordings reads the files record.sh writes.
package recordings

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultNow is past log_grace for every recorded attempt.
func DefaultNow() time.Time { return time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC) }

// Line is one request in status.txt. First and Final differ when the
// request was redirected.
type Line struct {
	First, Final int
	Path         string
}

// ParseStatus reads lines of "STATUS PATH" or "FIRST->FINAL PATH".
func ParseStatus(r io.Reader) ([]Line, error) {
	var lines []Line
	scanner := bufio.NewScanner(r)
	for n := 1; scanner.Scan(); n++ {
		line, err := parseLine(scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("line %d: %q: %w", n, scanner.Text(), err)
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}

func parseLine(text string) (Line, error) {
	codes, path, ok := strings.Cut(text, " ")
	if !ok || path == "" {
		return Line{}, fmt.Errorf("want STATUS PATH")
	}
	first, final, redirected := strings.Cut(codes, "->")
	if !redirected {
		final = first
	}
	var line Line
	var err error
	if line.First, err = strconv.Atoi(first); err != nil {
		return Line{}, err
	}
	if line.Final, err = strconv.Atoi(final); err != nil {
		return Line{}, err
	}
	line.Path = path
	return line, nil
}

// Root is testdata/recordings. It is found from this source file, so it
// works from any package's test binary.
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "recordings")
}

// Dir holds a run at a stage.
func Dir(runID int64, stage string) string {
	return filepath.Join(Root(), fmt.Sprintf("run-%d", runID), stage)
}
