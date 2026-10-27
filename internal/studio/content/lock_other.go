//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package content

import "os"

func lockRootFile(string) (*os.File, error) { return nil, ErrUnsupported }
