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

const (
	format     = "lg-store 1"
	rgignore   = "/state/\n/tmp/\n"
	unitPrefix = "unit-"
)

var (
	ErrExists = errors.New("unit already exists")
	ErrFormat = errors.New("unsupported store format")
)

// fsOps is the filesystem seam that faultfs wraps.
type fsOps interface {
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
	fs        fsOps
	data, tmp string
}

// Init makes root a store, finishing one that an earlier Init left part
// way. Callers hold state/write.lock. FORMAT is published last, so a store
// with one is complete.
func Init(root string) error { return initFS(OSFS{}, root) }

func initFS(fsys fsOps, root string) error {
	if err := check(fsys, root); err != nil {
		return err
	}
	s := &Store{fs: fsys, data: filepath.Join(root, "data"), tmp: filepath.Join(root, "tmp")}
	for _, dir := range []string{s.data, filepath.Join(root, "state"), s.tmp} {
		if _, err := mkdirAll(fsys, dir, true); err != nil {
			return err
		}
	}
	formatFile := filepath.Join(root, "FORMAT")
	if done, err := s.Has(formatFile); done || err != nil {
		return err
	}
	unit, err := s.NewUnit()
	if err != nil {
		return err
	}
	if has, err := s.Has(filepath.Join(root, ".rgignore")); err != nil {
		return err
	} else if !has {
		if err := unit.place(".rgignore", rgignore, root); err != nil {
			return err
		}
	}
	if err := unit.place("FORMAT", format+"\n", root); err != nil {
		return err
	}
	return unit.Abort()
}

// Check returns an error unless root is an lg-store 1 store, or holds only
// what Init writes before FORMAT.
func Check(root string) error { return check(OSFS{}, root) }

func check(fsys fsOps, root string) error {
	err := checkFormat(root)
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	entries, err := fsys.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		own, err := isOwn(fsys, root, e.Name())
		if err != nil {
			return err
		}
		if !own {
			return fmt.Errorf("%s has no FORMAT and holds files lg did not write; point LG_HOME at an empty or new dir", root)
		}
	}
	return nil
}

// isOwn reports whether name, in a root without FORMAT, is what Init or a
// writer waiting on state/write.lock left there.
func isOwn(fsys fsOps, root, name string) (bool, error) {
	var ownChild func(string) bool
	switch name {
	case ".rgignore":
		return true, nil
	case "data":
		ownChild = func(string) bool { return false }
	case "state":
		ownChild = func(child string) bool { return child == "write.lock" }
	case "tmp":
		ownChild = isUnit
	default:
		return false, nil
	}
	children, err := fsys.ReadDir(filepath.Join(root, name))
	if err != nil {
		return false, err
	}
	for _, child := range children {
		if !ownChild(child.Name()) {
			return false, nil
		}
	}
	return true, nil
}

func Open(root string) (*Store, error) { return openFS(OSFS{}, root) }

// openFS opens the store at root through fsys. It refuses a FORMAT other than
// lg-store 1, and a tmp/ that cannot be renamed into data/.
func openFS(fsys fsOps, root string) (*Store, error) {
	if err := checkFormat(root); err != nil {
		return nil, err
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

// checkFormat returns ErrFormat unless root's FORMAT is lg-store 1, and
// fs.ErrNotExist when root has no FORMAT.
func checkFormat(root string) error {
	formatFile := filepath.Join(root, "FORMAT")
	got, err := os.ReadFile(formatFile)
	if err != nil {
		return err
	}
	if v := strings.TrimSuffix(string(got), "\n"); v != format {
		return fmt.Errorf("%w: %s is %q; this lg reads %q", ErrFormat, formatFile, v, format)
	}
	return nil
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

// Sweep removes the units in tmp/, which a dead process left behind.
// Callers hold state/write.lock, so no live unit is there.
func (s *Store) Sweep() error {
	entries, err := s.fs.ReadDir(s.tmp)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !isUnit(e.Name()) {
			continue
		}
		if err := s.fs.RemoveAll(filepath.Join(s.tmp, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Unit is a directory staged under tmp/ until Publish renames it into data/.
type Unit struct {
	fs   fsOps
	dir  string
	dirs []string // created under dir, parents first
}

func (s *Store) NewUnit() (*Unit, error) {
	dir := filepath.Join(s.tmp, unitPrefix+strconv.FormatUint(rand.Uint64(), 36))
	if err := s.fs.Mkdir(dir); err != nil {
		return nil, err
	}
	return &Unit{fs: s.fs, dir: dir}, nil
}

func isUnit(name string) bool { return strings.HasPrefix(name, unitPrefix) }

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
func mkdirAll(fsys fsOps, path string, syncParents bool) ([]string, error) {
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

// place writes name in the unit, then renames it into dir, which must not
// have one, so a reader sees no file or the whole of it.
func (u *Unit) place(name, content, dir string) error {
	w, err := u.Create(name)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, content); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := u.fs.Rename(filepath.Join(u.dir, name), filepath.Join(dir, name)); err != nil {
		return err
	}
	return u.fs.SyncDir(dir)
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
