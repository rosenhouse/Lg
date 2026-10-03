// Package store stages units in tmp/ and publishes them into data/ whole.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

const format = "lg-store 1"

var (
	ErrExists = errors.New("unit already exists")
	ErrFormat = errors.New("unsupported store format")
)

// FS is the filesystem seam that faultfs wraps.
type FS interface {
	Mkdir(path string) error
	Create(path string) (File, error)
	Rename(oldpath, newpath string) error
	SyncDir(path string) error
	RemoveAll(path string) error
	ReadDir(path string) ([]fs.DirEntry, error)
	Lstat(path string) (fs.FileInfo, error)
	Device(path string) (uint64, error)
}

type File interface {
	io.Writer
	Sync() error
	Close() error
}

type Store struct {
	fs        FS
	data, tmp string
}

// Init creates a store at root unless root already has a FORMAT. FORMAT is
// written last, so a store with one is complete.
func Init(root string) error {
	formatFile := filepath.Join(root, "FORMAT")
	if _, err := os.Lstat(formatFile); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, dir := range []string{"data", "state", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return err
		}
	}
	if err := writeNew(filepath.Join(root, ".rgignore"), "state/\ntmp/\n"); err != nil {
		return err
	}
	return writeNew(formatFile, format+"\n")
}

// writeNew writes content to path unless path exists.
func writeNew(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func Open(root string) (*Store, error) { return OpenFS(OSFS{}, root) }

// OpenFS opens the store at root through fsys. It refuses a FORMAT other than
// lg-store 1, and a tmp/ that cannot be renamed into data/.
func OpenFS(fsys FS, root string) (*Store, error) {
	formatFile := filepath.Join(root, "FORMAT")
	got, err := os.ReadFile(formatFile)
	if err != nil {
		return nil, err
	}
	if v := strings.TrimSuffix(string(got), "\n"); v != format {
		return nil, fmt.Errorf("%w: %s is %q; this lg reads %q", ErrFormat, formatFile, v, format)
	}
	s := &Store{fs: fsys, data: filepath.Join(root, "data"), tmp: filepath.Join(root, "tmp")}
	dataDev, err := fsys.Device(s.data)
	if err != nil {
		return nil, err
	}
	tmpDev, err := fsys.Device(s.tmp)
	if err != nil {
		return nil, err
	}
	if dataDev != tmpDev {
		return nil, fmt.Errorf("%s and %s are on different devices, so units cannot be renamed into place", s.tmp, s.data)
	}
	return s, nil
}

func (s *Store) Data() string { return s.data }

// Has reports whether target exists.
func (s *Store) Has(target string) (bool, error) {
	_, err := s.fs.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Sweep removes everything in tmp/: units that a dead process left behind.
// Callers hold state/write.lock, so no live unit is there.
func (s *Store) Sweep() error {
	entries, err := s.fs.ReadDir(s.tmp)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := s.fs.RemoveAll(filepath.Join(s.tmp, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Unit is a directory staged under tmp/ until Publish renames it into data/.
type Unit struct {
	fs   FS
	dir  string
	dirs []string // created under dir, parents first
}

func (s *Store) NewUnit() (*Unit, error) {
	dir := filepath.Join(s.tmp, "unit-"+strconv.FormatUint(rand.Uint64(), 36))
	if err := s.fs.Mkdir(dir); err != nil {
		return nil, err
	}
	return &Unit{fs: s.fs, dir: dir}, nil
}

// Publish makes the unit durable, then renames it to target, which must not exist.
func (s *Store) Publish(u *Unit, target string) error {
	for _, dir := range slices.Backward(append([]string{u.dir}, u.dirs...)) {
		if err := s.fs.SyncDir(dir); err != nil {
			return err
		}
	}
	parent := filepath.Dir(target)
	if _, err := mkdirAll(s.fs, parent, true); err != nil {
		return err
	}
	if err := s.fs.Rename(u.dir, target); err != nil {
		if errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.ENOTEMPTY) {
			return fmt.Errorf("%w: %s", ErrExists, target)
		}
		return err
	}
	return s.fs.SyncDir(parent)
}

// mkdirAll makes path and any missing parents, returning those it made,
// parents first. With syncParents, it fsyncs each new dir's parent.
func mkdirAll(fsys FS, path string, syncParents bool) ([]string, error) {
	var missing []string
	for dir := path; ; dir = filepath.Dir(dir) {
		_, err := fsys.Lstat(dir)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, dir)
	}
	slices.Reverse(missing)
	for _, dir := range missing {
		if err := fsys.Mkdir(dir); err != nil {
			return nil, err
		}
		if syncParents {
			if err := fsys.SyncDir(filepath.Dir(dir)); err != nil {
				return nil, err
			}
		}
	}
	return missing, nil
}

// Abort removes the staged unit.
func (u *Unit) Abort() error { return u.fs.RemoveAll(u.dir) }

// Create opens a new member file, creating its parent dirs within the unit.
// Closing it fsyncs it.
func (u *Unit) Create(name string) (io.WriteCloser, error) {
	if !filepath.IsLocal(name) || slices.Contains(strings.Split(filepath.ToSlash(name), "/"), "..") {
		return nil, fmt.Errorf("member name %q is absolute or contains \"..\"", name)
	}
	path := filepath.Join(u.dir, name)
	made, err := mkdirAll(u.fs, filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	u.dirs = append(u.dirs, made...)
	f, err := u.fs.Create(path)
	if err != nil {
		return nil, err
	}
	return syncOnClose{f}, nil
}

type syncOnClose struct{ File }

func (f syncOnClose) Close() error {
	if err := f.Sync(); err != nil {
		_ = f.File.Close()
		return err
	}
	return f.File.Close()
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
