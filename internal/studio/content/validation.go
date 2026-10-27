package content

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

func validRevision(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validDocumentID(value string) bool {
	if value == "" || len(value) > 128 || !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789", rune(value[0])) {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789._-", char) {
			return false
		}
	}
	return true
}

func resolveLocation(absolute string) (string, error) {
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if current == absolute && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
				return "", fmt.Errorf("%w: managed locations must be real directories", ErrInvalid)
			}
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			relative, err := filepath.Rel(current, absolute)
			if err != nil {
				return "", err
			}
			return filepath.Join(resolved, relative), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(current) == current {
			return "", ErrInvalid
		}
	}
}

func checkPathCase(root *os.Root, name string) error {
	parent := "."
	for _, component := range strings.Split(name, "/") {
		directory, err := root.Open(parent)
		if os.IsNotExist(err) {
			return nil
		}
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
		found := false
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), component) {
				if entry.Name() != component {
					return fmt.Errorf("%w: case-folded path collision", ErrConflict)
				}
				found = true
			}
		}
		if !found {
			return nil
		}
		parent = path.Join(parent, component)
	}
	return nil
}

func foldName(value string) string {
	return strings.Map(func(char rune) rune {
		minimum := char
		for next := unicode.SimpleFold(char); next != char; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		return minimum
	}, value)
}

func validateJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= 50000 {
		return ErrLimit
	}
	*nodes = *nodes + 1
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: invalid manifest JSON", ErrInvalid)
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return ErrInvalid
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return fmt.Errorf("%w: duplicate manifest field", ErrInvalid)
			}
			keys[name] = true
			if err := validateJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := validateJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
	default:
		return ErrInvalid
	}
	if _, err := decoder.Token(); err != nil {
		return ErrInvalid
	}
	return nil
}
