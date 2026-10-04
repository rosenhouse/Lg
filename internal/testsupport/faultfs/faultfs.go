// Package faultfs wraps store.OSFS to journal its writes and to fail every op
// from a chosen one on, as if the process had died there.
package faultfs

import (
	"io/fs"
	"sync"

	"github.com/rosenhouse/lg/internal/store"
)

// Op is a journaled op: mkdir, create, write, fsync, close, rename or remove.
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
	journal  []Op
	mounts   map[string]store.Mount
}

func New() *FS { return &FS{mounts: map[string]store.Mount{}} }

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

// do journals op and runs it, unless it is op k or later.
func (f *FS) do(op Op, run func() error) error {
	f.mu.Lock()
	if f.failFrom > 0 && len(f.journal)+1 >= f.failFrom {
		f.mu.Unlock()
		return &fs.PathError{Op: op.Name, Path: op.Path, Err: f.err}
	}
	f.journal = append(f.journal, op)
	f.mu.Unlock()
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
	return f.do(Op{Name: "remove", Path: path}, func() error { return f.inner.RemoveAll(path) })
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
func (f *FS) FailOn(name string, err error) {}
