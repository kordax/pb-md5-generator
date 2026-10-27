//go:build !windows

package content

import (
	"os"
	"syscall"
)

func privateStateDirectory(info os.FileInfo) bool {
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(owner.Uid) == uint64(os.Geteuid()) && info.Mode().Perm()&0o077 == 0
}
