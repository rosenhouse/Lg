package store

import (
	"io/fs"

	"golang.org/x/sys/unix"
)

func mount(path string) (Mount, error) {
	var stx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, 0, unix.STATX_MNT_ID, &stx); err != nil {
		return Mount{}, &fs.PathError{Op: "statx", Path: path, Err: err}
	}
	return Mount{Dev: unix.Mkdev(stx.Dev_major, stx.Dev_minor), ID: stx.Mnt_id}, nil
}
