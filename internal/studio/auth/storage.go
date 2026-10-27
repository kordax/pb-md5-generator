package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/internal/studio/httpjson"
)

const stateFile = "auth.json"

func openService(config Config, options runtimeOptions) (*Service, error) {
	origin, secure, err := sameOrigin(config.PublicURL)
	if err != nil {
		return nil, err
	}
	if config.StateDir == "" || !filepath.IsAbs(config.StateDir) {
		return nil, fmt.Errorf("%w: authentication state directory must be absolute", ErrInvalid)
	}
	config.StateDir = filepath.Clean(config.StateDir)
	if config.SessionTTL == 0 {
		config.SessionTTL = 24 * time.Hour
	}
	if config.SessionTTL < 5*time.Minute || config.SessionTTL > maxSessionTTL {
		return nil, fmt.Errorf("%w: session lifetime must be between five minutes and thirty days", ErrInvalid)
	}
	if config.MaxUsers == 0 {
		config.MaxUsers = 128
	}
	if config.MaxSessions == 0 {
		config.MaxSessions = 1024
	}
	if config.MaxUsers < 1 || config.MaxUsers > maxUsers || config.MaxSessions < 1 || config.MaxSessions > maxSessions {
		return nil, fmt.Errorf("%w: authentication limits are out of range", ErrInvalid)
	}
	if options.iterations < 1 || options.iterations > maxPasswordIterations || options.now == nil || options.derive == nil {
		return nil, fmt.Errorf("%w: authentication runtime options are invalid", ErrInvalid)
	}
	if err := os.MkdirAll(config.StateDir, 0700); err != nil {
		return nil, fmt.Errorf("create authentication state directory: %w", err)
	}
	if err := CheckPrivateDirectory(config.StateDir); err != nil {
		return nil, err
	}
	lock, err := acquireLock(filepath.Join(config.StateDir, "auth.lock"))
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(config.StateDir)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("open authentication state directory: %w", err)
	}
	if err := checkOpenedDirectory(root); err != nil {
		_ = root.Close()
		_ = lock.Close()
		return nil, err
	}
	service := &Service{root: root, lock: lock, config: config, origin: origin, secure: secure, runtime: options, hashSlots: make(chan struct{}, 2), rates: make(map[string]rateEntry)}
	if err := service.loadState(); err != nil {
		_ = service.Close()
		return nil, err
	}
	return service, nil
}

// CheckPrivateDirectory verifies an existing directory without creating,
// changing permissions or following a final symlink. Unix ownership must match
// the effective user; Windows callers must provision a private NTFS ACL.
func CheckPrivateDirectory(name string) error {
	if name == "" {
		return fmt.Errorf("private directory path is required")
	}
	info, err := os.Lstat(filepath.Clean(name))
	if err != nil {
		return fmt.Errorf("inspect private directory: %w", err)
	}
	return validatePrivateDirectory(info)
}

func validatePrivateDirectory(info os.FileInfo) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("directory must be private, owned by the current user and not a symlink (mode 0700)")
	}
	return nil
}

func checkOpenedDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open private directory for validation: %w", err)
	}
	info, statErr := directory.Stat()
	closeErr := directory.Close()
	if statErr != nil || closeErr != nil {
		return fmt.Errorf("private directory cannot be validated")
	}
	return validatePrivateDirectory(info)
}

func (s *Service) loadState() error {
	info, err := s.root.Lstat(stateFile)
	if os.IsNotExist(err) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return ErrStorage
		}
		s.csrfKey = key
		state := diskState{Version: 1, CSRFKey: base64.RawStdEncoding.EncodeToString(key), Users: map[string]storedUser{}, Sessions: map[string]storedSession{}}
		return s.commitLocked(state)
	}
	if err != nil {
		return fmt.Errorf("read authentication state: %w", err)
	}
	if !info.Mode().IsRegular() || !ownedByCurrentUser(info) || info.Size() > maxStateBytes || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("authentication state must be a private regular file within its size limit")
	}
	file, err := s.root.Open(stateFile)
	if err != nil {
		return fmt.Errorf("open authentication state: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil || len(data) > maxStateBytes || !utf8.Valid(data) {
		return fmt.Errorf("authentication state exceeds its read limit or cannot be read")
	}
	if err := httpjson.Validate(data); err != nil {
		return fmt.Errorf("authentication state contains invalid JSON structure")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state diskState
	if err := decoder.Decode(&state); err != nil {
		return fmt.Errorf("authentication state is not valid versioned JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("authentication state contains trailing data")
	}
	if err := s.validateState(state); err != nil {
		return err
	}
	key, _ := base64.RawStdEncoding.DecodeString(state.CSRFKey)
	s.csrfKey, s.state = key, state
	return nil
}

func (s *Service) validateState(state diskState) error {
	invalid := func() error { return fmt.Errorf("authentication state failed integrity validation") }
	if state.Version != 1 || state.Users == nil || state.Sessions == nil || len(state.Users) > s.config.MaxUsers || len(state.Sessions) > s.config.MaxSessions {
		return invalid()
	}
	key, err := base64.RawStdEncoding.DecodeString(state.CSRFKey)
	if err != nil || len(key) != 32 {
		return invalid()
	}
	usernames := make(map[string]bool, len(state.Users))
	for id, user := range state.Users {
		if !validID(id) || user.ID != id || user.Version == 0 || user.Username != strings.ToLower(user.Username) || usernames[user.Username] {
			return invalid()
		}
		// Validate public fields without reusing or modifying the stored hash.
		candidate, err := validatedPublicUser(CreateUserRequest{Username: user.Username, DisplayName: user.DisplayName, Role: user.Role, Grants: user.Grants})
		if err != nil || candidate.Username != user.Username || candidate.DisplayName != user.DisplayName {
			return invalid()
		}
		if user.Password.Algorithm != "pbkdf2-sha256" || user.Password.Iterations < s.runtime.iterations || user.Password.Iterations > maxPasswordIterations {
			return invalid()
		}
		salt, saltErr := base64.RawStdEncoding.DecodeString(user.Password.Salt)
		hash, hashErr := base64.RawStdEncoding.DecodeString(user.Password.Hash)
		if saltErr != nil || hashErr != nil || len(salt) != 32 || len(hash) != 32 {
			return invalid()
		}
		usernames[user.Username] = true
	}
	if len(state.Users) != 0 && enabledAdmins(state) == 0 {
		return invalid()
	}
	for hash, session := range state.Sessions {
		digest, err := hex.DecodeString(hash)
		user, exists := state.Users[session.UserID]
		if err != nil || len(digest) != 32 || !exists || user.Disabled || user.Version != session.Version || session.Created.IsZero() || !session.Expires.After(session.Created) || session.Expires.Sub(session.Created) > maxSessionTTL {
			return invalid()
		}
	}
	return nil
}

func validID(id string) bool {
	value, err := hex.DecodeString(id)
	return err == nil && len(value) == 16 && strings.ToLower(id) == id
}

// commitLocked publishes a complete new snapshot before changing the in-memory
// authority. The temporary file is private, synced, closed and atomically renamed.
func (s *Service) commitLocked(next diskState) error {
	data, err := json.Marshal(next)
	if err != nil || len(data) > maxStateBytes {
		return ErrStorage
	}
	if err := httpjson.Validate(data); err != nil {
		return ErrStorage
	}
	name, err := randomValue(16)
	if err != nil {
		return ErrStorage
	}
	name = ".auth-" + name + ".tmp"
	file, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrStorage
	}
	defer func() { _ = s.root.Remove(name) }()
	written, writeErr := file.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return ErrStorage
	}
	if err := s.root.Rename(name, stateFile); err != nil {
		return ErrStorage
	}
	s.state = next
	return nil
}

type rateEntry struct {
	window time.Time
	count  int
}

func (s *Service) allowLogin(client, username string) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	now := s.runtime.now().UTC()
	window := now.Truncate(time.Minute)
	if !window.Equal(s.rateStart) {
		s.rateStart, s.rateCount = window, 0
	}
	if s.rateCount >= 60 {
		return false
	}
	// Never retain raw account names or caller-controlled forwarding headers.
	keys := []string{"ip:" + sessionHash(client), "account:" + sessionHash(strings.ToLower(username))}
	limits := []int{30, 5}
	for key, entry := range s.rates {
		if !entry.window.Equal(window) {
			delete(s.rates, key)
		}
	}
	for index, key := range keys {
		entry := s.rates[key]
		if entry.count >= limits[index] || entry.count == 0 && len(s.rates) >= 1024 {
			return false
		}
	}
	for _, key := range keys {
		entry := s.rates[key]
		entry.window, entry.count = window, entry.count+1
		s.rates[key] = entry
	}
	s.rateCount++
	return true
}
