package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/kordax/pb-md5-generator/internal/studio/httpjson"
)

func (s *Service) listUsers(actor *User) ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.adminLocked(actor); err != nil {
		return nil, err
	}
	users := make([]User, 0, len(s.state.Users))
	for _, record := range s.state.Users {
		user := record.User
		user.version = record.Version
		users = append(users, cloneUser(user))
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	return users, nil
}

func (s *Service) createUser(ctx context.Context, actor *User, request CreateUserRequest) (User, error) {
	user, err := validatedUser(request)
	if err != nil {
		return User{}, err
	}
	password, err := s.makePassword(ctx, request.Password)
	if err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.adminLocked(actor); err != nil {
		return User{}, err
	}
	if len(s.state.Users) >= s.config.MaxUsers {
		return User{}, ErrConflict
	}
	for _, existing := range s.state.Users {
		if existing.Username == user.Username {
			return User{}, ErrConflict
		}
	}
	user.ID, err = randomValue(16)
	if err != nil {
		return User{}, ErrStorage
	}
	user.version = 1
	next := cloneState(s.state)
	next.Users[user.ID] = storedUser{User: user, Version: 1, Password: password}
	if err := s.commitLocked(next); err != nil {
		return User{}, err
	}
	return cloneUser(user), nil
}

func parseGrantPatch(raw json.RawMessage) (*Grants, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var grants *Grants
	if err := httpjson.Unmarshal(raw, &grants); err != nil {
		return nil, fmt.Errorf("%w: grants must be a valid object or null", ErrInvalid)
	}
	return normalizeGrants(grants)
}

func (s *Service) updateUser(ctx context.Context, actor *User, id string, request UpdateUserRequest) (User, error) {
	if request.DisplayName == nil && request.Password == nil && request.Role == nil && request.Disabled == nil && len(request.Grants) == 0 {
		return User{}, fmt.Errorf("%w: provide at least one user field to update", ErrInvalid)
	}
	s.mu.RLock()
	if err := s.adminLocked(actor); err != nil {
		s.mu.RUnlock()
		return User{}, err
	}
	snapshot, exists := s.state.Users[id]
	s.mu.RUnlock()
	if !exists {
		return User{}, ErrNotFound
	}
	updated := snapshot
	updated.User = cloneUser(snapshot.User)
	if request.DisplayName != nil {
		updated.DisplayName = *request.DisplayName
	}
	if request.Role != nil {
		updated.Role = *request.Role
	}
	if request.Disabled != nil {
		updated.Disabled = *request.Disabled
	}
	if len(request.Grants) != 0 {
		grants, err := parseGrantPatch(request.Grants)
		if err != nil {
			return User{}, err
		}
		updated.Grants = grants
	}
	validated, err := validatedPublicUser(CreateUserRequest{Username: updated.Username, DisplayName: updated.DisplayName, Role: updated.Role, Grants: updated.Grants})
	if err != nil || validated.DisplayName != updated.DisplayName {
		return User{}, fmt.Errorf("%w: user fields are invalid", ErrInvalid)
	}
	if request.Password != nil {
		if err := validatePassword(*request.Password, updated.Username, updated.DisplayName); err != nil {
			return User{}, err
		}
		password, err := s.makePassword(ctx, *request.Password)
		if err != nil {
			return User{}, err
		}
		updated.Password = password
	}
	revoke := request.Password != nil || updated.Role != snapshot.Role || updated.Disabled != snapshot.Disabled || !reflect.DeepEqual(updated.Grants, snapshot.Grants)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.adminLocked(actor); err != nil {
		return User{}, err
	}
	current, exists := s.state.Users[id]
	if !exists {
		return User{}, ErrNotFound
	}
	if current.Version != snapshot.Version || current.DisplayName != snapshot.DisplayName {
		return User{}, ErrConflict
	}
	if revoke {
		if updated.Version == ^uint64(0) {
			return User{}, ErrStorage
		}
		updated.Version++
	}
	updated.version = updated.Version
	next := cloneState(s.state)
	next.Users[id] = updated
	if enabledAdmins(next) == 0 {
		return User{}, ErrLastAdmin
	}
	if revoke {
		revokeUserSessions(&next, id)
	}
	if err := s.commitLocked(next); err != nil {
		return User{}, err
	}
	return cloneUser(updated.User), nil
}

func (s *Service) deleteUser(actor *User, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.adminLocked(actor); err != nil {
		return err
	}
	if _, exists := s.state.Users[id]; !exists {
		return ErrNotFound
	}
	next := cloneState(s.state)
	delete(next.Users, id)
	if enabledAdmins(next) == 0 {
		return ErrLastAdmin
	}
	revokeUserSessions(&next, id)
	return s.commitLocked(next)
}

func (s *Service) logout(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	key := sessionHash(token)
	if _, exists := s.state.Sessions[key]; !exists {
		return nil
	}
	next := cloneState(s.state)
	delete(next.Sessions, key)
	return s.commitLocked(next)
}
