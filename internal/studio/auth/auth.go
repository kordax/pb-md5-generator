// Package auth provides bounded, persistent studio authentication. One process
// owns each private state directory; document authorization is enforced by the
// studio's content and reader handlers using the resolved user and Can.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MinPasswordIterations = 600000
	maxPasswordIterations = 2000000
	maxPasswordBytes      = 1024
	maxStateBytes         = 8 << 20
	maxUsers              = 256
	maxSessions           = 4096
	maxSessionsPerUser    = 16
	maxGrantPrefixes      = 32
	maxGrantPathBytes     = 256
	maxSessionTTL         = 30 * 24 * time.Hour
)

var (
	ErrUnauthorized = errors.New("authentication required")
	ErrForbidden    = errors.New("permission denied")
	ErrInvalid      = errors.New("invalid authentication request")
	ErrConflict     = errors.New("authentication state conflicts with the request")
	ErrNotFound     = errors.New("user not found")
	ErrBusy         = errors.New("password verification is busy")
	ErrRateLimited  = errors.New("too many login attempts")
	ErrStorage      = errors.New("authentication storage is unavailable")
	ErrClosed       = errors.New("authentication service is closed")
	ErrLastAdmin    = errors.New("at least one enabled administrator is required")
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

type Action string

const (
	ActionRead  Action = "read"
	ActionWrite Action = "write"
	ActionAdmin Action = "admin"
)

// Grants restrict role capabilities to project-relative segment prefixes. Nil
// means role defaults; a nonnil grants value with empty lists denies all paths.
// A write grant does not implicitly add a read grant or elevate a viewer.
type Grants struct {
	Read  []string `json:"read"`
	Write []string `json:"write"`
}

type User struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	Role        Role    `json:"role"`
	Disabled    bool    `json:"disabled"`
	Grants      *Grants `json:"grants,omitempty"`
	version     uint64
}

type Config struct {
	StateDir    string
	PublicURL   string
	SessionTTL  time.Duration
	MaxUsers    int
	MaxSessions int
}

type SessionResponse struct {
	User          *User  `json:"user"`
	CSRFToken     string `json:"csrfToken,omitempty"`
	SetupRequired bool   `json:"setupRequired,omitempty"`
}

type CreateUserRequest struct {
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	Password    string  `json:"password"`
	Role        Role    `json:"role"`
	Grants      *Grants `json:"grants,omitempty"`
}

type UpdateUserRequest struct {
	DisplayName *string         `json:"displayName,omitempty"`
	Password    *string         `json:"password,omitempty"`
	Role        *Role           `json:"role,omitempty"`
	Disabled    *bool           `json:"disabled,omitempty"`
	Grants      json.RawMessage `json:"grants,omitempty"`
}

type passwordRecord struct {
	Algorithm  string `json:"algorithm"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	Hash       string `json:"hash"`
}

type storedUser struct {
	User
	Password passwordRecord `json:"password"`
	Version  uint64         `json:"version"`
}

type storedSession struct {
	UserID  string    `json:"userId"`
	Version uint64    `json:"version"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

type diskState struct {
	Version  int                      `json:"version"`
	CSRFKey  string                   `json:"csrfKey"`
	Users    map[string]storedUser    `json:"users"`
	Sessions map[string]storedSession `json:"sessions"`
}

// runtimeOptions is deliberately private: production Open never accepts a lower
// password cost. Package tests may substitute a cheap KDF and deterministic clock.
type runtimeOptions struct {
	iterations int
	now        func() time.Time
	derive     func(string, []byte, int) ([]byte, error)
}

type Service struct {
	mu        sync.RWMutex
	state     diskState
	root      *os.Root
	lock      *os.File
	closed    bool
	config    Config
	origin    string
	secure    bool
	csrfKey   []byte
	runtime   runtimeOptions
	hashSlots chan struct{}
	rateMu    sync.Mutex
	rates     map[string]rateEntry
	rateStart time.Time
	rateCount int
}

func Open(config Config) (*Service, error) {
	return openService(config, runtimeOptions{iterations: MinPasswordIterations, now: time.Now, derive: derivePassword})
}

func derivePassword(password string, salt []byte, iterations int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, iterations, 32)
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	clear(s.csrfKey)
	return errors.Join(s.root.Close(), s.lock.Close())
}

func (s *Service) CookieName() string {
	if s.secure {
		return "__Host-docs_session"
	}
	return "docs_session"
}

func (s *Service) NeedsSetup() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.state.Users) == 0
}

func (s *Service) Bootstrap(ctx context.Context, username, displayName string, password []byte) (User, error) {
	request := CreateUserRequest{Username: username, DisplayName: displayName, Password: string(password), Role: RoleAdmin}
	user, err := validatedUser(request)
	if err != nil {
		return User{}, err
	}
	record, err := s.makePassword(ctx, request.Password)
	if err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return User{}, ErrClosed
	}
	if len(s.state.Users) != 0 {
		return User{}, ErrConflict
	}
	user.ID, err = randomValue(16)
	if err != nil {
		return User{}, ErrStorage
	}
	user.version = 1
	next := cloneState(s.state)
	next.Users[user.ID] = storedUser{User: user, Password: record, Version: 1}
	if err := s.commitLocked(next); err != nil {
		return User{}, err
	}
	return cloneUser(user), nil
}

func roleRank(role Role) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleEditor:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

func Can(user *User, action Action, projectPath string) bool {
	if user == nil || user.Disabled || roleRank(user.Role) == 0 {
		return false
	}
	if action == ActionAdmin {
		return user.Role == RoleAdmin
	}
	if action != ActionRead && action != ActionWrite || action == ActionWrite && roleRank(user.Role) < roleRank(RoleEditor) {
		return false
	}
	if !validProjectPath(projectPath) {
		return false
	}
	if user.Grants == nil {
		return true
	}
	prefixes := user.Grants.Read
	if action == ActionWrite {
		prefixes = user.Grants.Write
	}
	for _, prefix := range prefixes {
		if prefix == "." || projectPath == prefix || strings.HasPrefix(projectPath, prefix+"/") {
			return true
		}
	}
	return false
}

func validProjectPath(value string) bool {
	return value != "" && len(value) <= 4096 && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0 && !strings.ContainsAny(value, "\\\x00:") && !strings.HasPrefix(value, "/") && value != ".." && !strings.HasPrefix(value, "../") && path.Clean(value) == value
}

func normalizeGrants(grants *Grants) (*Grants, error) {
	if grants == nil {
		return nil, nil
	}
	result := &Grants{Read: make([]string, 0), Write: make([]string, 0)}
	for index, source := range [][]string{grants.Read, grants.Write} {
		if len(source) > maxGrantPrefixes {
			return nil, fmt.Errorf("%w: too many grant prefixes", ErrInvalid)
		}
		seen := map[string]bool{}
		for _, prefix := range source {
			if !validProjectPath(prefix) || len(prefix) > maxGrantPathBytes || strings.IndexFunc(prefix, unicode.IsControl) >= 0 {
				return nil, fmt.Errorf("%w: grant prefixes must be clean project-relative paths", ErrInvalid)
			}
			if seen[prefix] {
				continue
			}
			seen[prefix] = true
			if index == 0 {
				result.Read = append(result.Read, prefix)
			} else {
				result.Write = append(result.Write, prefix)
			}
		}
	}
	return result, nil
}

func validatePassword(password, username, displayName string) error {
	if !utf8.ValidString(password) || len(password) > maxPasswordBytes || utf8.RuneCountInString(password) < 10 {
		return fmt.Errorf("%w: password must contain at least 10 Unicode characters and at most 1024 UTF-8 bytes", ErrInvalid)
	}
	normalized := strings.ToLower(strings.TrimSpace(password))
	common := map[string]bool{
		"passwordpassword": true, "passwordpasswordpassword": true,
		"123456789012345": true, "1234567890123456": true,
		"qwertyuiopasdfgh": true, "qwertyuiopasdfghjkl": true,
		"correct horse battery staple": true, "letmeinletmeinletmein": true,
		"adminadminadmin": true, "administratoradministrator": true,
		"password123456789": true, "changemechangeme": true,
	}
	if normalized == "" || common[normalized] || normalized == strings.ToLower(username) || displayName != "" && normalized == strings.ToLower(displayName) {
		return fmt.Errorf("%w: choose a password that is not a common or account-derived value", ErrInvalid)
	}
	return nil
}

func validatedPublicUser(request CreateUserRequest) (User, error) {
	username := strings.ToLower(request.Username)
	if len(username) == 0 || len(username) > 64 {
		return User{}, fmt.Errorf("%w: username must contain 1–64 ASCII letters, digits, dots, underscores or hyphens", ErrInvalid)
	}
	for _, c := range []byte(username) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' {
			continue
		}
		return User{}, fmt.Errorf("%w: username contains an unsupported character", ErrInvalid)
	}
	name := request.DisplayName
	if name == "" {
		name = request.Username
	}
	if !utf8.ValidString(name) || len(name) > 256 || strings.TrimSpace(name) == "" || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return User{}, fmt.Errorf("%w: display name must contain 1–256 bytes of text without controls", ErrInvalid)
	}
	if roleRank(request.Role) == 0 {
		return User{}, fmt.Errorf("%w: role must be admin, editor or viewer", ErrInvalid)
	}
	grants, err := normalizeGrants(request.Grants)
	if err != nil {
		return User{}, err
	}
	return User{Username: username, DisplayName: name, Role: request.Role, Grants: grants}, nil
}

func validatedUser(request CreateUserRequest) (User, error) {
	user, err := validatedPublicUser(request)
	if err != nil {
		return User{}, err
	}
	if err := validatePassword(request.Password, user.Username, user.DisplayName); err != nil {
		return User{}, err
	}
	return user, nil
}

func cloneUser(user User) User {
	if user.Grants != nil {
		user.Grants = &Grants{Read: append([]string{}, user.Grants.Read...), Write: append([]string{}, user.Grants.Write...)}
	}
	return user
}

func cloneState(state diskState) diskState {
	result := diskState{Version: state.Version, CSRFKey: state.CSRFKey, Users: make(map[string]storedUser, len(state.Users)), Sessions: make(map[string]storedSession, len(state.Sessions))}
	for key, value := range state.Users {
		result.Users[key] = value
	}
	for key, value := range state.Sessions {
		result.Sessions[key] = value
	}
	return result
}

func randomValue(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func (s *Service) makePassword(ctx context.Context, password string) (passwordRecord, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return passwordRecord{}, ErrStorage
	}
	key, err := s.hash(ctx, password, salt, s.runtime.iterations)
	if err != nil {
		return passwordRecord{}, err
	}
	return passwordRecord{Algorithm: "pbkdf2-sha256", Iterations: s.runtime.iterations, Salt: base64.RawStdEncoding.EncodeToString(salt), Hash: base64.RawStdEncoding.EncodeToString(key)}, nil
}

func (s *Service) hash(ctx context.Context, password string, salt []byte, iterations int) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalid)
	}
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	default:
		return nil, ErrBusy
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := s.runtime.derive(password, salt, iterations)
	if err != nil || len(key) != 32 {
		return nil, ErrStorage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return key, nil
}

func sessionHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func (s *Service) csrfToken(token string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mac := hmac.New(sha256.New, s.csrfKey)
	_, _ = mac.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) login(ctx context.Context, username, password, client string) (User, string, error) {
	if !s.allowLogin(client, username) {
		return User{}, "", ErrRateLimited
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return User{}, "", ErrClosed
	}
	var snapshot storedUser
	found := false
	for _, candidate := range s.state.Users {
		if candidate.Username == strings.ToLower(username) {
			snapshot, found = candidate, true
			break
		}
	}
	s.mu.RUnlock()
	passwordHash := snapshot.Password
	if !found {
		passwordHash = passwordRecord{Iterations: s.runtime.iterations, Salt: base64.RawStdEncoding.EncodeToString(make([]byte, 32)), Hash: base64.RawStdEncoding.EncodeToString(make([]byte, 32))}
	}
	salt, _ := base64.RawStdEncoding.DecodeString(passwordHash.Salt)
	expected, _ := base64.RawStdEncoding.DecodeString(passwordHash.Hash)
	actual, err := s.hash(ctx, password, salt, passwordHash.Iterations)
	if err != nil {
		return User{}, "", err
	}
	matches := subtle.ConstantTimeCompare(actual, expected) == 1
	if !found || !matches || snapshot.Disabled {
		return User{}, "", ErrUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return User{}, "", ErrClosed
	}
	current, exists := s.state.Users[snapshot.ID]
	if !exists || current.Disabled || current.Version != snapshot.Version {
		return User{}, "", ErrUnauthorized
	}
	token, err := randomValue(32)
	if err != nil {
		return User{}, "", ErrStorage
	}
	now := s.runtime.now().UTC()
	next := cloneState(s.state)
	pruneSessions(&next, now)
	trimUserSessions(&next, current.ID)
	if len(next.Sessions) >= s.config.MaxSessions {
		return User{}, "", ErrBusy
	}
	next.Sessions[sessionHash(token)] = storedSession{UserID: current.ID, Version: current.Version, Created: now, Expires: now.Add(s.config.SessionTTL)}
	if err := s.commitLocked(next); err != nil {
		return User{}, "", err
	}
	current.version = current.Version
	return cloneUser(current.User), token, nil
}

func pruneSessions(state *diskState, now time.Time) {
	for key, session := range state.Sessions {
		if !now.Before(session.Expires) {
			delete(state.Sessions, key)
		}
	}
}

func trimUserSessions(state *diskState, userID string) {
	type sessionTime struct {
		key string
		at  time.Time
	}
	var sessions []sessionTime
	for key, session := range state.Sessions {
		if session.UserID == userID {
			sessions = append(sessions, sessionTime{key: key, at: session.Created})
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].at.Equal(sessions[j].at) {
			return sessions[i].key < sessions[j].key
		}
		return sessions[i].at.Before(sessions[j].at)
	})
	for len(sessions) >= maxSessionsPerUser {
		delete(state.Sessions, sessions[0].key)
		sessions = sessions[1:]
	}
}

func revokeUserSessions(state *diskState, id string) {
	for key, session := range state.Sessions {
		if session.UserID == id {
			delete(state.Sessions, key)
		}
	}
}

func enabledAdmins(state diskState) int {
	count := 0
	for _, user := range state.Users {
		if user.Role == RoleAdmin && !user.Disabled {
			count++
		}
	}
	return count
}

func (s *Service) adminLocked(actor *User) error {
	if s.closed {
		return ErrClosed
	}
	if actor == nil {
		return ErrUnauthorized
	}
	current, exists := s.state.Users[actor.ID]
	if !exists || current.Disabled || current.Version != actor.version {
		return ErrUnauthorized
	}
	if current.Role != RoleAdmin {
		return ErrForbidden
	}
	return nil
}

func sameOrigin(publicURL string) (string, bool, error) {
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", false, fmt.Errorf("%w: public URL must be an HTTP(S) origin with an optional clean path", ErrInvalid)
	}
	if parsed.Path != "" && parsed.Path != "/" && (path.Clean(parsed.Path) != strings.TrimSuffix(parsed.Path, "/") || strings.Contains(parsed.Path, "\\")) {
		return "", false, fmt.Errorf("%w: public URL path must be clean", ErrInvalid)
	}
	if parsed.Scheme == "http" && !strings.EqualFold(parsed.Hostname(), "localhost") {
		return "", false, fmt.Errorf("%w: HTTP public URLs require the literal localhost hostname; use HTTPS for other hosts", ErrInvalid)
	}
	return canonicalOrigin(parsed), parsed.Scheme == "https", nil
}
