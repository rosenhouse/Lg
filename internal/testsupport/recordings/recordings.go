// Package recordings reads the files record.sh writes.
package recordings

import "io"

// Line is one request in status.txt. First and Final differ when the
// request was redirected.
type Line struct {
	First, Final int
	Path         string
}

func ParseStatus(r io.Reader) ([]Line, error) { return nil, nil }
