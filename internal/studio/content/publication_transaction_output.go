package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type publicationReader struct {
	ctx context.Context
	r   io.Reader
}

func (r publicationReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(data)
}

// Output has a separate compiler-owned namespace and a 512 MiB budget. Hash
// streams one file at a time instead of retaining the compiled site in memory.
func publicationOutputDigest(ctx context.Context, directory string, allowEmpty bool) (string, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.Join(ErrOwnership, err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	digest := sha256.New()
	_, _ = io.WriteString(digest, "pb-docs-output/v1\x00")
	var count int
	var total int64
	seen := make(map[string]bool)
	var walk func(string, int) error
	walk = func(name string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 64 {
			return ErrLimit
		}
		if err := noSymlinks(root, name); err != nil {
			return err
		}
		directory, err := root.Open(name)
		if err != nil {
			return err
		}
		entries, err := readPublicationDirectoryEntries(directory, 4*MaxFiles+1)
		if err != nil {
			return err
		}
		count += len(entries)
		if count > 4*MaxFiles {
			return ErrLimit
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := path.Join(name, entry.Name())
			folded := foldName(child)
			if !fs.ValidPath(child) || strings.Contains(child, "\\") || seen[folded] || entry.Type()&os.ModeSymlink != 0 {
				return ErrOwnership
			}
			seen[folded] = true
			if entry.IsDir() {
				_, _ = io.WriteString(digest, "d\x00"+child+"\x00")
				if err := walk(child, depth+1); err != nil {
					return err
				}
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return errors.Join(ErrOwnership, err)
			}
			if info.Size() < 0 || info.Size() > (512<<20)-total {
				return ErrLimit
			}
			total += info.Size()
			if err := noSymlinks(root, child); err != nil {
				return err
			}
			file, err := root.Open(child)
			if err != nil {
				return err
			}
			opened, statErr := file.Stat()
			if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || info.Size() != opened.Size() {
				return errors.Join(ErrOwnership, statErr, file.Close())
			}
			hash := sha256.New()
			size, readErr := io.Copy(hash, io.LimitReader(publicationReader{ctx: ctx, r: file}, info.Size()+1))
			closedInfo, statErr := file.Stat()
			closeErr := file.Close()
			if readErr != nil || statErr != nil || closeErr != nil || size != info.Size() || !os.SameFile(info, closedInfo) || info.Size() != closedInfo.Size() || !info.ModTime().Equal(closedInfo.ModTime()) {
				return errors.Join(ErrOwnership, readErr, statErr, closeErr)
			}
			_, _ = io.WriteString(digest, "f\x00"+child+"\x00"+hex.EncodeToString(hash.Sum(nil))+"\x00")
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return "", err
	}
	if err := validateOutput(directory, allowEmpty); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func publicationOutputPrefix(output string) string {
	hash := sha256.Sum256([]byte(output))
	return ".studio-publication-" + hex.EncodeToString(hash[:16]) + "-"
}

func (t *publicationTransaction) outputHolder() string {
	return publicationOutputPrefix(t.service.cfg.OutputDir) + t.journal.ID
}
func (t *publicationTransaction) outputParent() string { return filepath.Dir(t.service.cfg.OutputDir) }

func (t *publicationTransaction) checkOutputHolder(ctx context.Context) (bool, error) {
	exists, err := storagePathExists(t.outputParent(), t.outputHolder())
	if err != nil || !exists {
		return exists, err
	}
	owner, err := readSource(t.outputParent(), t.outputHolder()+"/.storage-owner", 128)
	if err != nil || string(owner) != t.journal.ID {
		return true, errors.Join(ErrOwnership, err)
	}
	root, err := os.OpenRoot(t.outputParent())
	if err != nil {
		return true, err
	}
	defer root.Close()
	file, err := root.Open(t.outputHolder())
	if err != nil {
		return true, err
	}
	entries, err := readPublicationDirectoryEntries(file, 4)
	if err != nil {
		return true, err
	}
	if len(entries) > 3 {
		return true, ErrOwnership
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".storage-owner" && entry.Type().IsRegular() {
			continue
		}
		if name != "previous" && name != "after" || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return true, ErrOwnership
		}
		expected := t.journal.OutputBefore
		if name == "after" {
			expected = t.journal.OutputAfter
		}
		actual, err := publicationOutputDigest(ctx, filepath.Join(t.outputParent(), t.outputHolder(), name), name == "previous")
		if err != nil || actual != expected {
			return true, errors.Join(ErrOwnership, err)
		}
	}
	return true, nil
}

// Both locations are configuration-owned or derived from the validated journal.
// A common os.Root confines the rename, including its missing destination.
func renamePublication(from, to string) (bool, error) {
	ancestor := filepath.Dir(from)
	for !contains(ancestor, to) {
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return false, ErrOwnership
		}
		ancestor = next
	}
	root, err := os.OpenRoot(ancestor)
	if err != nil {
		return false, err
	}
	defer root.Close()
	oldName, err := filepath.Rel(ancestor, from)
	if err != nil {
		return false, err
	}
	newName, err := filepath.Rel(ancestor, to)
	if err != nil {
		return false, err
	}
	for _, name := range []string{oldName, newName} {
		if err := noSymlinks(root, filepath.ToSlash(name)); err != nil {
			return false, err
		}
	}
	if _, err := root.Lstat(newName); !errors.Is(err, os.ErrNotExist) {
		return false, errors.Join(ErrOwnership, err)
	}
	if err := root.Rename(oldName, newName); err != nil {
		return false, err
	}
	return true, errors.Join(syncStorageDirectory(ancestor, filepath.ToSlash(filepath.Dir(oldName))), syncStorageDirectory(ancestor, filepath.ToSlash(filepath.Dir(newName))))
}

func (t *publicationTransaction) replaceOutput() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.service.cfg.OperationTimeout)
	defer cancel()
	if err := t.authority(ctx, false); err != nil {
		return false, err
	}
	current, err := t.currentSource(ctx)
	if err != nil || current.Revision != t.journal.After {
		return false, errors.Join(ErrOwnership, err)
	}
	if err := t.nextHistoryMatches(ctx); err != nil {
		return false, err
	}
	old, err := publicationOutputDigest(ctx, t.service.cfg.OutputDir, true)
	if err != nil || old != t.journal.OutputBefore {
		return false, errors.Join(ErrOwnership, err)
	}
	staged := filepath.Join(t.directory(), "site")
	next, err := publicationOutputDigest(ctx, staged, false)
	if err != nil || next != t.journal.OutputAfter {
		return false, errors.Join(ErrOwnership, err)
	}
	root, err := os.OpenRoot(t.outputParent())
	if err != nil {
		return false, err
	}
	defer root.Close()
	if err := root.Mkdir(t.outputHolder(), 0o700); err != nil {
		return false, err
	}
	if err := writeStorageBytes(t.outputParent(), t.outputHolder()+"/.storage-owner", []byte(t.journal.ID)); err != nil {
		return false, err
	}
	if err := syncStorageDirectory(t.outputParent(), "."); err != nil {
		return false, err
	}
	previous := filepath.Join(t.outputParent(), t.outputHolder(), "previous")
	if _, err := renamePublication(t.service.cfg.OutputDir, previous); err != nil {
		return false, err
	}
	return renamePublication(staged, t.service.cfg.OutputDir)
}

func (t *publicationTransaction) rollbackOutput(ctx context.Context) error {
	if _, err := t.checkOutputHolder(ctx); err != nil {
		return err
	}
	output := t.service.cfg.OutputDir
	previous := filepath.Join(t.outputParent(), t.outputHolder(), "previous")
	hasPrevious, err := storagePathExists(t.outputParent(), t.outputHolder()+"/previous")
	if err != nil {
		return err
	}
	current, err := publicationOutputDigest(ctx, output, true)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !hasPrevious {
		if err != nil || current != t.journal.OutputBefore {
			return errors.Join(ErrOwnership, err)
		}
		return nil
	}
	if err == nil {
		if current != t.journal.OutputAfter {
			return ErrOwnership
		}
		retained := filepath.Join(t.outputParent(), t.outputHolder(), "after")
		if _, err := renamePublication(output, retained); err != nil {
			return err
		}
	}
	_, err = renamePublication(previous, output)
	return err
}
