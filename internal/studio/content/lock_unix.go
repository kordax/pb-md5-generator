//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package content

import (
	"errors"
	"os"
	"syscall"
)

func lockRootFile(name string) (*os.File, error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(ErrConflict, err, file.Close())
	}
	return file, nil
}
