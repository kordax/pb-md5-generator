//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package content

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func lockRootFile(name string) (*os.File, error) {
	directory, err := os.OpenRoot(filepath.Dir(name))
	if err != nil {
		return nil, err
	}
	file, openErr := directory.OpenFile(filepath.Base(name), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	closeErr := directory.Close()
	if openErr != nil {
		return nil, errors.Join(openErr, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, file.Close())
	}
	info, err := file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if !info.Mode().IsRegular() || !privateStateDirectory(info) {
		return nil, errors.Join(ErrOwnership, file.Close())
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(ErrConflict, err, file.Close())
	}
	return file, nil
}
