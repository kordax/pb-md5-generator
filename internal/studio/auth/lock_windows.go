//go:build windows

package auth

import (
	"fmt"
	"os"
	"syscall"
)

// Unix mode bits do not model NTFS ACLs; the state directory must inherit a
// private operator-managed ACL. Exclusive process ownership is enforced below.
func ownedByCurrentUser(_ os.FileInfo) bool { return true }

func acquireLock(name string) (*os.File, error) {
	if info, err := os.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("authentication lock must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect authentication lock: %w", err)
	}
	pointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("authentication lock path is invalid")
	}
	// Sharing mode zero gives the process exclusive ownership. Closing the file
	// or terminating the process releases it without leaving a stale lease.
	handle, err := syscall.CreateFile(pointer, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("authentication state directory is unavailable or already owned by another process")
	}
	return os.NewFile(uintptr(handle), name), nil
}
