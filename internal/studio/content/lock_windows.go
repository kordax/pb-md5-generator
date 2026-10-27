package content

import (
	"errors"
	"os"
	"syscall"
)

func lockRootFile(name string) (*os.File, error) {
	pointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(pointer, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, errors.Join(ErrConflict, err)
	}
	return os.NewFile(uintptr(handle), name), nil
}
