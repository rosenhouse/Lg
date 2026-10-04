package mirror

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rosenhouse/lg/internal/store"
)

// stateFile is a JSON hint under state/ that lg can lose without losing data.
type stateFile struct {
	store *store.Store
	path  string
}

func newStateFile(s *store.Store, name string) stateFile {
	return stateFile{store: s, path: filepath.Join(s.State(), name)}
}

// read decodes the file. A missing file decodes nothing. One that decode
// rejects is moved aside, and read gives decode's error as discarded.
func (f stateFile) read(decode func([]byte) error) (discarded, err error) {
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := decode(raw); err != nil {
		aside := f.path + ".corrupt"
		if err := f.store.Rename(f.path, aside); err != nil {
			return nil, err
		}
		return &corruptFileError{Path: f.path, Err: fmt.Errorf("moved to %s: %w", aside, err)}, nil
	}
	return nil, nil
}

// write replaces the file with v as compact JSON, with <, > and & as they are.
func (f stateFile) write(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return f.store.ReplaceFile(f.path, buf.Bytes())
}
