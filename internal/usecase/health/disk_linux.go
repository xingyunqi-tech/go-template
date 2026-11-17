package health

import "syscall"

func blockSize(st *syscall.Statfs_t) uint64 {
	return uint64(st.Bsize) //nolint:gosec // statfs reports a positive block size
}
