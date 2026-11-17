//go:build !linux

package health

import "syscall"

func blockSize(st *syscall.Statfs_t) uint64 {
	return uint64(st.Bsize)
}
