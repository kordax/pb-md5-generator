package content

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const maxStorageJSONBytes = 8 << 20

func decodeStorageJSON(data []byte, result any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

func syncStorageDirectory(directory, name string) error {
	// Windows does not provide directory fsync through os.File.Sync. File data
	// is still synced before atomic renames on every supported platform.
	if runtime.GOOS == "windows" {
		return nil
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := noSymlinks(root, name); err != nil {
		return err
	}
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func writeStorageBytes(directory, name string, data []byte) error {
	if err := writeSource(directory, name, data); err != nil {
		return err
	}
	return syncStorageDirectory(directory, path.Dir(name))
}

func writeStorageJSON(directory, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > maxStorageJSONBytes {
		return ErrLimit
	}
	total, err := stateSize(directory)
	if err != nil {
		return err
	}
	if old, err := readSource(directory, name, maxStorageJSONBytes); err == nil {
		total -= int64(len(old))
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if total+int64(len(data)) > MaxStateBytes {
		return ErrLimit
	}
	return writeStorageBytes(directory, name, data)
}

func removeStorageFile(directory, name string) error {
	if err := removeSource(directory, name); err != nil {
		return err
	}
	return syncStorageDirectory(directory, path.Dir(name))
}

func removeStorageTree(directory, name string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := noSymlinks(root, name); err != nil {
		return err
	}
	if err := root.RemoveAll(name); err != nil {
		return err
	}
	return syncStorageDirectory(directory, path.Dir(name))
}

// syncStorageTree bounds both allocation and Git metadata before a prepared
// repository can become authoritative. No symlinks or special files are kept.
func syncStorageTree(ctx context.Context, directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	var count int
	var size int64
	var walk func(string, int) error
	walk = func(name string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 64 {
			return ErrLimit
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		entries, readErr := file.ReadDir(MaxFiles*4 + 1)
		closeErr := file.Close()
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		count += len(entries)
		if count > MaxFiles*4 {
			return ErrLimit
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := path.Join(name, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				return ErrOwnership
			}
			if entry.IsDir() {
				if err := walk(child, depth+1); err != nil {
					return err
				}
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return errors.Join(ErrOwnership, err)
			}
			size += info.Size()
			if size > 2*MaxProjectBytes {
				return ErrLimit
			}
			opened, err := root.Open(child)
			if err != nil {
				return err
			}
			if err := errors.Join(opened.Sync(), opened.Close()); err != nil {
				return err
			}
		}
		return syncStorageDirectory(directory, name)
	}
	return walk(".", 0)
}

// Each preparation lives in a reserved, owned namespace. The sentinel is
// durable before any source or Git objects are copied into it.
func createStorageStage(state, prefix string) (string, string, error) {
	id, err := randomID()
	if err != nil {
		return "", "", err
	}
	name := prefix + id
	root, err := os.OpenRoot(state)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	if err := root.Mkdir(name, 0o700); err != nil {
		return "", "", err
	}
	stage := filepath.Join(state, name)
	if err := writeStorageBytes(stage, ".storage-owner", []byte(id)); err != nil {
		_ = root.RemoveAll(name)
		return "", "", err
	}
	if err := syncStorageDirectory(state, "."); err != nil {
		_ = root.RemoveAll(name)
		return "", "", err
	}
	return stage, id, nil
}

func cleanupStorageOrphans(ctx context.Context, state string) error {
	root, err := os.OpenRoot(state)
	if err != nil {
		return err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(MaxFiles + 1)
	closeErr := directory.Close()
	if readErr != nil && readErr != io.EOF {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(entries) > MaxFiles {
		return ErrLimit
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		id := ""
		for _, prefix := range []string{".txn-storage-", ".txn-configuration-"} {
			if strings.HasPrefix(name, prefix) {
				id = strings.TrimPrefix(name, prefix)
				break
			}
		}
		if !validID(id) {
			continue
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return ErrOwnership
		}
		stage := filepath.Join(state, name)
		marker, err := readSource(stage, ".storage-owner", 64)
		if errors.Is(err, ErrNotFound) {
			opened, err := root.Open(name)
			if err != nil {
				return err
			}
			children, readErr := opened.ReadDir(1)
			closeErr := opened.Close()
			if readErr != nil && readErr != io.EOF || closeErr != nil || len(children) != 0 {
				return ErrOwnership
			}
			if err := root.Remove(name); err != nil {
				return err
			}
		} else {
			if err != nil || string(marker) != id {
				return ErrOwnership
			}
			if err := root.RemoveAll(name); err != nil {
				return err
			}
		}
	}
	return syncStorageDirectory(state, ".")
}

func storagePathExists(directory, name string) (bool, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return false, err
	}
	defer root.Close()
	if err := noSymlinks(root, name); err != nil {
		return false, err
	}
	_, err = root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
