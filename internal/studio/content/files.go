package content

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/site"
)

type fileState struct {
	Entry
	Hash string
	Info fs.FileInfo
}

type sourceTree struct {
	Files    map[string]fileState
	Dirs     []string
	Revision string
}

func validPath(name string) bool {
	if name == "." || len(name) > 4096 || !fs.ValidPath(name) || !utf8.ValidString(name) {
		return false
	}
	for _, char := range name {
		if unicode.IsControl(char) || strings.ContainsRune("\\:?*\"<>|#%", char) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		lower := strings.ToLower(part)
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || lower == "_docs" || lower == "secret-values.yaml" || lower == "secret-values.yml" || lower == "credentials.json" || lower == "credentials.yaml" {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return false
		}
	}
	return true
}

func allowedFile(name string) bool {
	if !validPath(name) {
		return false
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".proto", ".json", ".yaml", ".yml", ".html", ".htm", ".txt", ".go", ".js", ".ts", ".sh", ".sql", ".css", ".xml", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".ico", ".pdf":
		return true
	default:
		return false
	}
}

// assetFile is deliberately narrower than editable source files. Code examples
// remain inert source and cannot become executable reader assets.
func assetFile(name string) bool {
	if !allowedFile(name) {
		return false
	}
	if binaryFile(name) {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".txt", ".json", ".yaml", ".yml", ".proto":
		return true
	default:
		return false
	}
}

func binaryFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".ico", ".pdf":
		return true
	default:
		return false
	}
}

func validOutput(name string) bool {
	if !validPath(name) || !strings.HasSuffix(name, ".html") || strings.HasPrefix(name, "assets/") {
		return false
	}
	for _, char := range name {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789/._-", char) {
			return false
		}
	}
	return true
}

func noSymlinks(root *os.Root, name string) error {
	for current := name; current != "."; current = path.Dir(current) {
		info, err := root.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlinks are not managed content", ErrInvalid)
		}
	}
	return nil
}

func readSource(directory, name string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := noSymlinks(root, name); err != nil {
		return nil, err
	}
	info, err := root.Stat(name)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrLimit
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrLimit
	}
	return data, nil
}

func writeSource(directory, name string, data []byte) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := noSymlinks(root, name); err != nil {
		return err
	}
	if err := root.MkdirAll(path.Dir(name), 0o750); err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	temporary := path.Join(path.Dir(name), ".studio-"+id)
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temporary) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return root.Rename(temporary, name)
}

func removeSource(directory, name string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := noSymlinks(root, name); err != nil {
		return err
	}
	if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// walkSource bounds directory enumeration before allocating an arbitrary listing.
func walkSource(ctx context.Context, root *os.Root, visit fs.WalkDirFunc) error {
	visited := 0
	var walk func(string, int) error
	walk = func(parent string, depth int) error {
		if depth > 64 {
			return ErrLimit
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		directory, err := root.Open(parent)
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
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			name := path.Join(parent, entry.Name())
			if !validPath(name) {
				continue
			}
			visited++
			if visited > MaxFiles {
				return ErrLimit
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(name, entry, nil); err != nil {
				if err == fs.SkipDir {
					continue
				}
				return err
			}
			if entry.IsDir() {
				if err := walk(name, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(".", 0)
}

func storageIdentity(status SyncStatus) string { return status.Branch + "\x00" + status.Head }

func scanTree(ctx context.Context, directory, branch, destination string) (sourceTree, error) {
	return scanTreeCached(ctx, directory, branch, destination, nil)
}

func scanTreeCached(ctx context.Context, directory, branch, destination string, cached map[string]fileState) (sourceTree, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return sourceTree{}, err
	}
	defer root.Close()
	result := sourceTree{Files: make(map[string]fileState)}
	hash := sha256.New()
	_, _ = io.WriteString(hash, branch+"\x00")
	var total int64
	seen := make(map[string]bool)
	err = walkSource(ctx, root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if !validPath(name) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlinks are not managed content", ErrInvalid)
		}
		folded := foldName(name)
		if seen[folded] {
			return fmt.Errorf("%w: case-folded source collision", ErrInvalid)
		}
		seen[folded] = true
		if entry.IsDir() {
			if len(result.Dirs)+len(result.Files) >= MaxFiles {
				return ErrLimit
			}
			result.Dirs = append(result.Dirs, name)
			_, _ = io.WriteString(hash, "d\x00"+name+"\x00")
			if destination != "" {
				return os.MkdirAll(filepath.Join(destination, filepath.FromSlash(name)), 0o750)
			}
			return nil
		}
		if !allowedFile(name) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		limit := int64(MaxTextBytes)
		if binaryFile(name) {
			limit = MaxBinaryBytes
		}
		if name == "docs.json" {
			limit = 1 << 20
		}
		if !info.Mode().IsRegular() || info.Size() > limit || total+info.Size() > MaxProjectBytes || len(result.Files)+len(result.Dirs) >= MaxFiles {
			return ErrLimit
		}
		var data []byte
		previous, reusable := cached[name]
		value, size := previous.Hash, info.Size()
		if destination != "" || !reusable || previous.Info == nil || previous.Info.Size() != info.Size() || !previous.Info.ModTime().Equal(info.ModTime()) || !os.SameFile(previous.Info, info) || !reflect.DeepEqual(previous.Info.Sys(), info.Sys()) {
			data, err = readSource(directory, name, limit)
			if err != nil {
				return err
			}
			if !binaryFile(name) && !utf8.Valid(data) {
				return fmt.Errorf("%w: text must be UTF-8", ErrInvalid)
			}
			digest := sha256.Sum256(data)
			value, size = hex.EncodeToString(digest[:]), int64(len(data))
			info, err = root.Stat(name)
			if err != nil || !info.Mode().IsRegular() || info.Size() != size {
				return errors.Join(ErrConflict, err)
			}
		}
		if total+size > MaxProjectBytes {
			return ErrLimit
		}
		result.Files[name] = fileState{Entry: Entry{Path: name, Size: size}, Hash: value, Info: info}
		total += size
		_, _ = io.WriteString(hash, "f\x00"+name+"\x00"+value+"\x00")
		if destination != "" {
			return writeSource(destination, name, data)
		}
		return nil
	})
	if err != nil {
		return sourceTree{}, err
	}
	result.Revision = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func loadManifest(directory string) (site.Manifest, error) {
	data, err := readSource(directory, "docs.json", 1<<20)
	if err != nil {
		return site.Manifest{}, err
	}
	if !utf8.Valid(data) {
		return site.Manifest{}, ErrInvalid
	}
	var nodes int
	if err := validateJSON(json.NewDecoder(strings.NewReader(string(data))), 0, &nodes); err != nil {
		return site.Manifest{}, err
	}
	var manifest site.Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("%w: invalid docs.json", ErrInvalid)
	}
	if decoder.Decode(new(any)) != io.EOF || manifest.Version != 1 || strings.TrimSpace(manifest.Title) == "" || len(manifest.Title) > 256 || len(manifest.Description) > 4096 || len(manifest.Pages) == 0 || len(manifest.Pages) > 2048 || len(manifest.Assets) > 256 {
		return manifest, fmt.Errorf("%w: invalid docs.json", ErrInvalid)
	}
	seen := make(map[string]bool)
	home := false
	for i := range manifest.Pages {
		page := &manifest.Pages[i]
		if !allowedFile(page.Source) || !validOutput(page.Path) || strings.TrimSpace(page.Title) == "" || len(page.Title) > 256 || seen["s:"+strings.ToLower(page.Source)] || seen["p:"+page.Path] {
			return manifest, fmt.Errorf("%w: invalid page metadata", ErrInvalid)
		}
		seen["s:"+strings.ToLower(page.Source)], seen["p:"+page.Path] = true, true
		if page.ID == "" {
			page.ID = document.PageID(page.Path)
		}
		if !validDocumentID(page.ID) || seen["id:"+page.ID] {
			return manifest, fmt.Errorf("%w: duplicate page identity", ErrInvalid)
		}
		seen["id:"+page.ID] = true
		if page.Kind == "" {
			page.Kind = "markdown"
		}
		if !validKind(page.Kind) {
			return manifest, fmt.Errorf("%w: unsupported document format", ErrInvalid)
		}
		home = home || page.Path == "index.html"
	}
	if !home {
		return manifest, fmt.Errorf("%w: index.html homepage is required", ErrInvalid)
	}
	for _, asset := range manifest.Assets {
		if !assetFile(asset) || seen["a:"+strings.ToLower(asset)] {
			return manifest, fmt.Errorf("%w: invalid attachment", ErrInvalid)
		}
		seen["a:"+strings.ToLower(asset)] = true
	}
	return manifest, nil
}

func validKind(kind string) bool {
	return kind == "markdown" || kind == "protobuf" || kind == "openapi" || kind == "html"
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && id == strings.ToLower(id)
}

func sortedFiles(tree sourceTree) []string {
	files := make([]string, 0, len(tree.Files))
	for name := range tree.Files {
		files = append(files, name)
	}
	sort.Strings(files)
	return files
}

func contains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
