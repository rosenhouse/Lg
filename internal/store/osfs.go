package store

import (
	"io/fs"
	"os"
	"syscall"
)

// OSFS is the real filesystem.
type OSFS struct{}

// Mkdir lets the umask apply, unlike os.MkdirTemp, which always uses 0700.
func (OSFS) Mkdir(path string) error { return os.Mkdir(path, 0o755) }

func (OSFS) Create(path string) (File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
}

func (OSFS) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

func (OSFS) SyncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}

func (OSFS) RemoveAll(path string) error { return os.RemoveAll(path) }

func (OSFS) ReadDir(path string) ([]fs.DirEntry, error) { return os.ReadDir(path) }

func (OSFS) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }

func (OSFS) Mount(path string) (Mount, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return Mount{}, &fs.PathError{Op: "stat", Path: path, Err: err}
	}
	return Mount{Dev: device(&st)}, nil
}
