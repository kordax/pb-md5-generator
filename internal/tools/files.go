package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func ProtoFilesRecursively(directory string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".proto" {
			result = append(result, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func RequireRegularFile(file string) error {
	stat, err := os.Stat(file)
	if err != nil {
		return fmt.Errorf("cannot process file %s: %w", file, err)
	}
	if stat.IsDir() {
		return fmt.Errorf("invalid path specified, path is a dir: %s", file)
	}
	return nil
}

func EnsureDirectoryAbsent(directory string) error {
	stat, err := os.Stat(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if stat.IsDir() {
		return fmt.Errorf("directory already exists: %s", directory)
	}
	return nil
}
