//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	uid := os.Geteuid()
	return ok && stat != nil && uid >= 0 && uint64(stat.Uid) == uint64(uid)
}

func acquireLock(name string) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Dir(name))
	if err != nil {
		return nil, fmt.Errorf("open authentication lock directory: %w", err)
	}
	defer root.Close()
	name = filepath.Base(name)
	if info, err := root.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("authentication lock must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect authentication lock: %w", err)
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open authentication lock: %w", err)
	}
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() || !ownedByCurrentUser(info) || info.Mode().Perm()&0077 != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("authentication lock must be private (mode 0600)")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("authentication state directory is already owned by another process")
	}
	return file, nil
}
