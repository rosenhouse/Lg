package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ReadState decodes state/<name>. A missing file decodes nothing. One that
// decode rejects is moved aside, and ReadState gives decode's error as
// discarded. Callers hold state/write.lock.
func (s *Store) ReadState(name string, decode func([]byte) error) (discarded, err error) {
	path := filepath.Join(s.state, name)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := decode(raw); err != nil {
		aside := path + ".corrupt"
		if err := s.Rename(path, aside); err != nil {
			return nil, err
		}
		return &CorruptFileError{Path: path, Err: fmt.Errorf("moved to %s: %w", aside, err)}, nil
	}
	return nil, nil
}

// WriteState replaces state/<name> with v as compact JSON, with <, > and &
// as they are. Callers hold state/write.lock.
func (s *Store) WriteState(name string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return s.ReplaceFile(filepath.Join(s.state, name), buf.Bytes())
}

// CorruptFileError is a file in the store that lg cannot parse.
type CorruptFileError struct {
	Path string
	Err  error
}

func (e *CorruptFileError) Error() string { return e.Path + ": " + e.Err.Error() }

func (e *CorruptFileError) Unwrap() error { return e.Err }
