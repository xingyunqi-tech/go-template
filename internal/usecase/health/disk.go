package health

import (
	"fmt"
	"syscall"
)

func diskUsage(path string) (total, available uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("health: statfs %s: %w", path, err)
	}

	bsize := blockSize(&st)

	return st.Blocks * bsize, st.Bavail * bsize, nil
}
