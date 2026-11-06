//go:build !windows

package receipts

import (
	"os"
	"syscall"
)

func owned(info os.FileInfo, singleLink bool) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(os.Geteuid()) && (!singleLink || stat.Nlink == 1)
}
