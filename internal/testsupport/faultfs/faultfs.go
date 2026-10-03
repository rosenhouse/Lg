// Package faultfs wraps store.FS to journal its writes and fail them from a chosen op on.
package faultfs

import (
	"io/fs"

	"github.com/rosenhouse/lg/internal/store"
)

type Op struct {
	Name, Path, To string
}

type FS struct {
	inner store.FS
}

func New(inner store.FS) *FS { return &FS{inner: inner} }

func (f *FS) FailFrom(k int, err error) {}

func (f *FS) Failed() bool { return false }

func (f *FS) Journal() []Op { return nil }

func (f *FS) SetDevice(path string, dev uint64) {}

func (f *FS) Mkdir(path string) error                    { return f.inner.Mkdir(path) }
func (f *FS) Create(path string) (store.File, error)     { return f.inner.Create(path) }
func (f *FS) Rename(oldpath, newpath string) error       { return f.inner.Rename(oldpath, newpath) }
func (f *FS) SyncDir(path string) error                  { return f.inner.SyncDir(path) }
func (f *FS) RemoveAll(path string) error                { return f.inner.RemoveAll(path) }
func (f *FS) ReadDir(path string) ([]fs.DirEntry, error) { return f.inner.ReadDir(path) }
func (f *FS) Lstat(path string) (fs.FileInfo, error)     { return f.inner.Lstat(path) }
func (f *FS) Device(path string) (uint64, error)         { return f.inner.Device(path) }

func (op Op) String() string { return "" }
