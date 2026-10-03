// Package store stages units in tmp/ and publishes them into data/ whole.
package store

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

type Store struct {
	tmp string
}

func New(tmp string) *Store { return &Store{tmp: tmp} }

// Unit is a directory staged under tmp/ until Publish renames it into data/.
type Unit struct {
	dir string
}

func (s *Store) NewUnit() (*Unit, error) {
	if err := os.MkdirAll(s.tmp, 0o755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(s.tmp, "unit-")
	if err != nil {
		return nil, err
	}
	return &Unit{dir: dir}, nil
}

func (s *Store) Publish(u *Unit, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Rename(u.dir, target)
}

// Create opens a new member file, creating its parent dirs within the unit.
func (u *Unit) Create(name string) (io.WriteCloser, error) {
	path := filepath.Join(u.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
}

// WriteJSON stores raw indented two spaces, plus a newline, so each key sits
// on its own line for grep.
func (u *Unit) WriteJSON(name string, raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	w, err := u.Create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}
