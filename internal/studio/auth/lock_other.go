//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package auth

import (
	"fmt"
	"os"
)

func ownedByCurrentUser(_ os.FileInfo) bool { return false }

func acquireLock(_ string) (*os.File, error) {
	return nil, fmt.Errorf("persistent studio authentication is unsupported on this operating system")
}
