package site

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

func validateManagedOutput(output string) error {
	info, err := os.Lstat(output)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output must be a directory, not a file or symlink")
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	root, err := os.OpenRoot(output)
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := readLimited(root, inventoryPath, maxManifestBytes)
	if err != nil {
		return fmt.Errorf("refusing to replace nonempty unmanaged output: %w", err)
	}
	var previous inventory
	if err := json.Unmarshal(data, &previous); err != nil || previous.Format != "pb-docs-site/v1" || len(previous.Files) > 2*maxPages+maxAssets+16 {
		return fmt.Errorf("output has an invalid build inventory")
	}
	for _, name := range previous.Files {
		if !localPath(name) {
			return fmt.Errorf("output inventory contains an invalid path")
		}
	}
	return checkFileSet(output, previous.Files)
}

func checkFileSet(directory string, files []string) error {
	known := make(map[string]bool, len(files))
	directories := map[string]bool{".": true}
	for _, name := range files {
		known[name] = true
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			directories[parent] = true
		}
	}
	return filepath.WalkDir(directory, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(directory, file)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if !directories[relative] {
				return fmt.Errorf("output contains unmanaged directory %s", relative)
			}
			return nil
		}
		if !entry.Type().IsRegular() || !known[relative] {
			return fmt.Errorf("output contains unmanaged file or symlink %s", relative)
		}
		return nil
	})
}

func publishStage(stage, output string) error {
	if err := validateManagedOutput(output); err != nil {
		return err
	}
	if _, err := os.Lstat(output); errors.Is(err, fs.ErrNotExist) {
		return os.Rename(stage, output)
	}
	backup, err := os.MkdirTemp(filepath.Dir(output), ".docs-previous-")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(output, backup); err != nil {
		return err
	}
	if err := os.Rename(stage, output); err != nil {
		if rollbackErr := os.Rename(backup, output); rollbackErr != nil {
			return fmt.Errorf("publish: %w; previous output is retained at %s: %v", err, backup, rollbackErr)
		}
		return err
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("site published; remove previous output %s: %w", backup, err)
	}
	return nil
}
