// Package faultfs wraps store.OSFS to journal its writes and to fail every op
// from a chosen one on, as if the process had died there.
package faultfs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/rosenhouse/lg/internal/store"
)

// Op is a journaled op: mkdir, create, write, fsync, close, rename, remove or chmod.
type Op struct {
	Name, Path, To string
}

func (op Op) String() string {
	if op.To != "" {
		return op.Name + " " + op.Path + " " + op.To
	}
	return op.Name + " " + op.Path
}

type FS struct {
	inner store.OSFS

	mu       sync.Mutex
	failFrom int
	err      error
	failOn   map[string]scopedErr
	before   map[string]scopedHook
	journal  []Op
	mounts   map[string]store.Mount
	nonRoot  bool
}

func New() *FS {
	return &FS{mounts: map[string]store.Mount{}, failOn: map[string]scopedErr{}, before: map[string]scopedHook{}}
}

// FailFrom makes the k-th journaled op, counting from 1, and every op after it return err.
func (f *FS) FailFrom(k int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failFrom, f.err = k, err
}

// Journal lists the mutating ops that reached the disk.
func (f *FS) Journal() []Op {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Op(nil), f.journal...)
}

func (f *FS) SetMount(path string, m store.Mount) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mounts[path] = m
}

// do journals op and runs it, unless it is op k or later or FailOn names it.
func (f *FS) do(op Op, run func() error) error {
	f.mu.Lock()
	if f.failFrom > 0 && len(f.journal)+1 >= f.failFrom {
		f.mu.Unlock()
		return &fs.PathError{Op: op.Name, Path: op.Path, Err: f.err}
	}
	if scoped, ok := f.failOn[op.Name]; ok && (under(op.Path, scoped.dir) || under(op.To, scoped.dir)) {
		f.mu.Unlock()
		return &fs.PathError{Op: op.Name, Path: op.Path, Err: scoped.err}
	}
	hook, hooked := f.before[op.Name]
	f.journal = append(f.journal, op)
	f.mu.Unlock()
	if hooked && (under(op.Path, hook.dir) || under(op.To, hook.dir)) {
		hook.run()
	}
	return run()
}

func (f *FS) Mkdir(path string) error {
	return f.do(Op{Name: "mkdir", Path: path}, func() error { return f.inner.Mkdir(path) })
}

func (f *FS) Create(path string) (store.File, error) {
	var file store.File
	err := f.do(Op{Name: "create", Path: path}, func() (err error) {
		file, err = f.inner.Create(path)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &faultFile{fs: f, path: path, inner: file}, nil
}

func (f *FS) Rename(oldpath, newpath string) error {
	return f.do(Op{Name: "rename", Path: oldpath, To: newpath}, func() error { return f.inner.Rename(oldpath, newpath) })
}

func (f *FS) SyncDir(path string) error {
	return f.do(Op{Name: "fsync", Path: path}, func() error { return f.inner.SyncDir(path) })
}

func (f *FS) RemoveAll(path string) error {
	return f.do(Op{Name: "remove", Path: path}, func() error {
		f.mu.Lock()
		nonRoot := f.nonRoot
		f.mu.Unlock()
		if dir := readOnlyDir(path); nonRoot && dir != "" {
			return &fs.PathError{Op: "unlinkat", Path: dir, Err: syscall.EACCES}
		}
		return f.inner.RemoveAll(path)
	})
}

func (f *FS) Chmod(path string, mode fs.FileMode) error {
	return f.do(Op{Name: "chmod", Path: path}, func() error { return f.inner.Chmod(path, mode) })
}

// ActAsNonRoot makes remove fail with EACCES, as it does for a user other than
// root, while the tree holds a non-empty dir without owner write permission.
func (f *FS) ActAsNonRoot() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nonRoot = true
}

// readOnlyDir gives a non-empty dir in the tree at root that lacks owner
// write permission, and "" when there is none.
func readOnlyDir(root string) string {
	var found string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		entries, _ := os.ReadDir(path)
		if infoErr == nil && info.Mode().Perm()&0o200 == 0 && len(entries) > 0 {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func (f *FS) ReadDir(path string) ([]fs.DirEntry, error) { return f.inner.ReadDir(path) }

func (f *FS) Lstat(path string) (fs.FileInfo, error) { return f.inner.Lstat(path) }

func (f *FS) Mount(path string) (store.Mount, error) {
	f.mu.Lock()
	m, ok := f.mounts[path]
	f.mu.Unlock()
	if ok {
		return m, nil
	}
	return f.inner.Mount(path)
}

type faultFile struct {
	fs    *FS
	path  string
	inner store.File
}

func (w *faultFile) Write(p []byte) (n int, err error) {
	err = w.fs.do(Op{Name: "write", Path: w.path}, func() error {
		n, err = w.inner.Write(p)
		return err
	})
	return n, err
}

func (w *faultFile) Sync() error {
	return w.fs.do(Op{Name: "fsync", Path: w.path}, w.inner.Sync)
}

func (w *faultFile) Close() error {
	return w.fs.do(Op{Name: "close", Path: w.path}, w.inner.Close)
}

// FailOn makes every later op named name return err.
func (f *FS) FailOn(name string, err error) {
	f.FailOnUnder(name, "/", err)
}

// FailOnUnder makes every later op named name whose path or rename target is
// under dir return err.
func (f *FS) FailOnUnder(name, dir string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failOn[name] = scopedErr{dir: dir, err: err}
}

// Before runs hook before every later op named name whose path or rename
// target is under dir.
func (f *FS) Before(name, dir string, hook func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.before[name] = scopedHook{dir: dir, run: hook}
}

type scopedHook struct {
	dir string
	run func()
}

type scopedErr struct {
	dir string
	err error
}

func under(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return path != "" && err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
