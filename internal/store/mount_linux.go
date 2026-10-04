package store

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/unix"
)

func mount(path string) (Mount, error) { return mountWith(unix.Statx, path) }

func mountWith(statx func(dirfd int, path string, flags, mask int, stat *unix.Statx_t) error, path string) (Mount, error) {
	var stx unix.Statx_t
	err := statx(unix.AT_FDCWD, path, 0, unix.STATX_MNT_ID, &stx)
	// Kernels before 4.11 and some seccomp profiles lack statx, so bind
	// mounts of one device go untold there, as on darwin.
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) {
		var st unix.Stat_t
		if err := unix.Stat(path, &st); err != nil {
			return Mount{}, &fs.PathError{Op: "stat", Path: path, Err: err}
		}
		return Mount{Dev: st.Dev}, nil
	}
	if err != nil {
		return Mount{}, &fs.PathError{Op: "statx", Path: path, Err: err}
	}
	return Mount{Dev: unix.Mkdev(stx.Dev_major, stx.Dev_minor), ID: stx.Mnt_id}, nil
}
