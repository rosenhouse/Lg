package store

import (
	"io/fs"
	"syscall"
)

func mount(path string) (Mount, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return Mount{}, &fs.PathError{Op: "stat", Path: path, Err: err}
	}
	return Mount{Dev: uint64(st.Dev)}, nil
}
