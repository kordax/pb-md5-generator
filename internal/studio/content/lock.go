package content

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func acquireRootLock(directory string) (*os.File, error) {
	return acquireManagedLockFile(filepath.Join(directory, ".studio-lock"))
}

func acquireManagedLockFile(name string) (*os.File, error) {
	if info, err := os.Lstat(name); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !privateStateDirectory(info) {
			return nil, fmt.Errorf("%w: invalid managed location lock", ErrOwnership)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return lockRootFile(name)
}

func acquireManagedLocks(root, state, output string) ([]*os.File, error) {
	var locks []*os.File
	fail := func(err error) ([]*os.File, error) {
		for _, lock := range locks {
			err = errors.Join(err, lock.Close())
		}
		return nil, err
	}
	for _, directory := range []string{root, state} {
		lock, err := acquireRootLock(directory)
		if err != nil {
			return fail(err)
		}
		locks = append(locks, lock)
	}
	// The output directory is atomically replaced when publishing. Its lock
	// lives beside it so every managed service locks the same inode.
	digest := sha256.Sum256([]byte(output))
	name := filepath.Join(filepath.Dir(output), ".pb-docs-output-"+hex.EncodeToString(digest[:16])+".lock")
	lock, err := acquireManagedLockFile(name)
	if err != nil {
		return fail(err)
	}
	return append(locks, lock), nil
}
