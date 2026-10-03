package store

import "syscall"

func device(st *syscall.Stat_t) uint64 { return uint64(st.Dev) }
