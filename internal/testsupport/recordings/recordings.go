// Package recordings reads the files record.sh writes.
package recordings

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/rosenhouse/lg/internal/model"
	"github.com/rosenhouse/lg/internal/tombstone"
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

// RecordedAt is when record.sh fetched the stage, from its recorded_at.txt.
func RecordedAt(runID int64, stage string) (time.Time, error) {
	raw, err := os.ReadFile(filepath.Join(Dir(runID, stage), "recorded_at.txt"))
	if err != nil {
		return time.Time{}, err
	}
	at, err := time.Parse(time.RFC1123, strings.TrimSpace(string(raw)))
	if err != nil {
		return time.Time{}, err
	}
	return at.UTC(), nil
}

// Artifacts is the listing in a stage's artifacts.json.
func Artifacts(runID int64, stage string) ([]model.Artifact, error) {
	raw, err := os.ReadFile(filepath.Join(Dir(runID, stage), "artifacts.json"))
	if err != nil {
		return nil, err
	}
	var listing struct{ Artifacts []model.Artifact }
	if err := json.Unmarshal(raw, &listing); err != nil {
		return nil, err
	}
	return listing.Artifacts, nil
}

// Attempt is a stage's attempt-N/attempt.json.
func Attempt(runID int64, stage string, attempt int) (model.Run, error) {
	var run model.Run
	err := readJSON(attemptFile(runID, stage, attempt, "attempt.json"), &run)
	return run, err
}

// Jobs is the listing in a stage's attempt-N/jobs.json.
func Jobs(runID int64, stage string, attempt int) ([]model.Job, error) {
	var listing struct{ Jobs []model.Job }
	err := readJSON(attemptFile(runID, stage, attempt, "jobs.json"), &listing)
	return listing.Jobs, err
}

func attemptFile(runID int64, stage string, attempt int, name string) string {
	return filepath.Join(Dir(runID, stage), fmt.Sprintf("attempt-%d", attempt), name)
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// CompareLog accepts log.txt in jobDir byte-identical to want, or a
// tombstone saying GitHub no longer serves the log.
func CompareLog(want []byte, jobDir string) error {
	logPath := filepath.Join(jobDir, "log.txt")
	got, err := os.ReadFile(logPath)
	switch {
	case err == nil && bytes.Equal(got, want):
		return nil
	case err == nil:
		return fmt.Errorf("%s differs from the recording", logPath)
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	var stone tombstone.Tombstone
	if err := readJSON(logPath+".tombstone", &stone); err != nil {
		return err
	}
	if stone.Reason != tombstone.Expired && stone.Reason != tombstone.Deleted {
		return fmt.Errorf("%s.tombstone has reason %q, not expired or deleted", logPath, stone.Reason)
	}
	return nil
}
