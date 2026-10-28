package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ManagedBackend keeps one working tree and history. Connections share that
// history; only the selected pull connection may update the working tree.
// Service serializes management with document operations. The mutex also keeps
// direct Backend callers from observing a partially installed configuration.
type ManagedBackend struct {
	mu             sync.Mutex
	root           string
	state          string
	timeout        time.Duration
	initialBackend string
	initialRemote  string
	settings       storedStorage
	states         []storedRemoteStatus
	active         Backend
	warning        string
	recoveryFailed bool
}

func NewManagedBackend(config ManagedStorageConfig) (*ManagedBackend, error) {
	if config.Root == "" || config.StateDir == "" {
		return nil, ErrInvalid
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, err
	}
	state, err := filepath.Abs(config.StateDir)
	if err != nil || contains(root, state) || contains(state, root) {
		return nil, ErrInvalid
	}
	if config.InitialBackend == "" {
		config.InitialBackend = "local"
	}
	if config.InitialBackend != "local" && config.InitialBackend != "git" {
		return nil, ErrInvalid
	}
	if config.InitialRemote == "" {
		config.InitialRemote = "origin"
	}
	if !validRemoteName(config.InitialRemote) {
		return nil, ErrInvalid
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Timeout < time.Second || config.Timeout > 2*time.Minute {
		return nil, ErrInvalid
	}
	return &ManagedBackend{root: root, state: state, timeout: config.Timeout, initialBackend: config.InitialBackend, initialRemote: config.InitialRemote, states: []storedRemoteStatus{}}, nil
}

func (m *ManagedBackend) ManagedRoot() string { return m.root }

func (m *ManagedBackend) managedState() string { return m.state }

func validRemoteName(name string) bool {
	if len(name) == 0 || len(name) > 64 || !validPath(name) {
		return false
	}
	for i, char := range name {
		alphanumeric := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
		if !alphanumeric && (i == 0 || char != '_' && char != '-') {
			return false
		}
	}
	return true
}

func validStorageBranch(name string) bool {
	return gitBranchArgument(name) && validPath(name) && !strings.Contains(name, "..") && !strings.Contains(name, "@{") && !strings.HasSuffix(strings.ToLower(name), ".lock")
}

func validateStorageSettings(settings StorageSettings) error {
	if settings.Backend != "local" && settings.Backend != "git" || settings.Remotes == nil || len(settings.Remotes) > MaxStorageRemotes {
		return ErrInvalid
	}
	if settings.Backend == "local" && (len(settings.Remotes) != 0 || settings.PullRemote != "") {
		return ErrInvalid
	}
	seen := make(map[string]bool)
	pullFound := settings.PullRemote == ""
	for _, remote := range settings.Remotes {
		if !validRemoteName(remote.Name) || !validStorageURL(remote.URL) || strings.IndexFunc(remote.URL, unicode.IsControl) >= 0 {
			return ErrInvalid
		}
		key := strings.ToLower(remote.Name)
		if seen[key] {
			return ErrInvalid
		}
		seen[key] = true
		pullFound = pullFound || settings.PullRemote == remote.Name
	}
	if !pullFound {
		return ErrInvalid
	}
	return nil
}

func storageDigest(settings storedStorage) (string, error) {
	settings.Revision = ""
	data, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func cloneStorageSettings(settings StorageSettings) StorageSettings {
	settings.Remotes = append([]StorageRemote{}, settings.Remotes...)
	return settings
}

func (m *ManagedBackend) settingsLocked() StorageSettings {
	return cloneStorageSettings(m.settings.StorageSettings)
}

func (m *ManagedBackend) remoteLocked(name string) (StorageRemote, error) {
	for _, remote := range m.settings.Remotes {
		if remote.Name == name {
			return remote, nil
		}
	}
	return StorageRemote{}, ErrNotFound
}

func (m *ManagedBackend) gitLocked(name string) (*GitBackend, error) {
	if m.active == nil || m.settings.Backend != "git" {
		return nil, ErrUnsupported
	}
	return &GitBackend{root: m.root, remote: name, timeout: m.timeout}, nil
}

func (m *ManagedBackend) Status(ctx context.Context) (SyncStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(ctx)
}

func (m *ManagedBackend) statusLocked(ctx context.Context) (SyncStatus, error) {
	if m.recoveryFailed {
		return SyncStatus{}, fmt.Errorf("%w: storage recovery is required; documents and drafts are retained", ErrConflict)
	}
	if m.active == nil {
		return SyncStatus{}, ErrOwnership
	}
	status, err := m.active.Status(ctx)
	if err != nil || status.Backend != "git" {
		return status, err
	}
	status.Ahead, status.Behind, status.Retryable = 0, 0, false
	status.State = "clean"
	if status.Dirty {
		status.State = "modified"
	}
	if m.settings.PullRemote != "" && validStorageBranch(status.Branch) {
		git := &GitBackend{root: m.root, remote: m.settings.PullRemote, timeout: m.timeout}
		counts, countErr := git.run(ctx, "rev-list", "--left-right", "--count", "HEAD...refs/remotes/"+m.settings.PullRemote+"/"+status.Branch)
		if countErr == nil {
			fields := strings.Fields(string(counts))
			if len(fields) == 2 {
				status.Ahead, _ = strconv.Atoi(fields[0])
				status.Behind, _ = strconv.Atoi(fields[1])
			}
		}
		if status.Behind > 0 && !status.Dirty {
			status.State = "behind"
		}
	}
	for _, remote := range m.settings.Remotes {
		if !remote.Push {
			continue
		}
		state := m.remoteStatusLocked(remote, status)
		if state.State == "pending" || state.State == "error" {
			status.State, status.Retryable = "pending", true
		}
	}
	if m.warning != "" {
		status.Error = m.warning
	}
	return status, nil
}

func (m *ManagedBackend) Branches(ctx context.Context) ([]Branch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return nil, ErrOwnership
	}
	return m.active.Branches(ctx)
}

func (m *ManagedBackend) CreateBranch(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return ErrOwnership
	}
	return m.active.CreateBranch(ctx, name)
}

func (m *ManagedBackend) Checkout(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return ErrOwnership
	}
	return m.active.Checkout(ctx, name)
}

func (m *ManagedBackend) Fetch(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.PullRemote == "" {
		return ErrUnsupported
	}
	git, err := m.gitLocked(m.settings.PullRemote)
	if err != nil {
		return err
	}
	return git.Fetch(ctx)
}

func (m *ManagedBackend) Pull(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.PullRemote == "" {
		return ErrUnsupported
	}
	git, err := m.gitLocked(m.settings.PullRemote)
	if err != nil {
		return err
	}
	return git.Pull(ctx)
}

func (m *ManagedBackend) RestoreHead(ctx context.Context, head string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return ErrOwnership
	}
	return m.active.RestoreHead(ctx, head)
}

func (m *ManagedBackend) Commit(ctx context.Context, paths []string, actor Actor, message string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return "", ErrOwnership
	}
	head, err := m.active.Commit(ctx, paths, actor, message)
	if err != nil || m.settings.Backend != "git" {
		return head, err
	}
	status, err := m.active.Status(ctx)
	if err != nil {
		return head, err
	}
	for _, remote := range m.settings.Remotes {
		if !remote.Push {
			continue
		}
		state := m.remoteStatusLocked(remote, status)
		if state.LastPushedHead != head {
			state.Head, state.State, state.Retryable, state.Error = head, "pending", true, ""
		}
		m.setRemoteStatusLocked(remote, state)
	}
	return head, m.persistRemoteStatesLocked()
}

func (m *ManagedBackend) Push(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.settings.Backend != "git" {
		return ErrUnsupported
	}
	var failures []error
	for _, remote := range m.settings.Remotes {
		if !remote.Push {
			continue
		}
		result, err := m.pushRemoteLocked(ctx, remote)
		if err != nil {
			failures = append(failures, err)
		} else if result.State == "error" {
			failures = append(failures, errors.New("remote push failed; local commit is retained"))
		}
	}
	return errors.Join(failures...)
}

func (m *ManagedBackend) remoteStatusLocked(remote StorageRemote, current SyncStatus) RemoteSyncStatus {
	result := RemoteSyncStatus{Name: remote.Name, Branch: current.Branch, Head: current.Head, State: "unknown"}
	for _, state := range m.states {
		if state.Name == remote.Name && state.URL == remote.URL && state.Branch == current.Branch {
			result = state.RemoteSyncStatus
			result.Head = current.Head
			break
		}
	}
	if !remote.Push {
		result.State, result.Retryable = "disabled", false
	} else if current.Head != "" && result.LastPushedHead != current.Head && result.State != "error" {
		result.State, result.Retryable = "pending", true
	}
	return result
}

func (m *ManagedBackend) remoteStatusesLocked(current SyncStatus) []RemoteSyncStatus {
	result := make([]RemoteSyncStatus, 0, len(m.settings.Remotes))
	for _, remote := range m.settings.Remotes {
		result = append(result, m.remoteStatusLocked(remote, current))
	}
	return result
}

func (m *ManagedBackend) setRemoteStatusLocked(remote StorageRemote, status RemoteSyncStatus) {
	for i, state := range m.states {
		if state.Name == remote.Name && state.Branch == status.Branch {
			m.states[i] = storedRemoteStatus{URL: remote.URL, RemoteSyncStatus: status}
			return
		}
	}
	count, oldest := 0, -1
	for i, state := range m.states {
		if state.Name == remote.Name {
			count++
			if oldest < 0 {
				oldest = i
			}
		}
	}
	if count >= 256 {
		// Eviction loses only a cached acknowledgement. Returning to that
		// branch reconstructs a conservative pending state from its Git HEAD.
		m.states = append(m.states[:oldest], m.states[oldest+1:]...)
	}
	m.states = append(m.states, storedRemoteStatus{URL: remote.URL, RemoteSyncStatus: status})
}

func (m *ManagedBackend) reconcileRemoteStatesLocked() {
	retained := make([]storedRemoteStatus, 0, len(m.states))
	for _, state := range m.states {
		for _, remote := range m.settings.Remotes {
			if state.Name == remote.Name && state.URL == remote.URL {
				retained = append(retained, state)
				break
			}
		}
	}
	m.states = retained
}

func (m *ManagedBackend) persistRemoteStatesLocked() error {
	if len(m.states) > MaxStorageRemotes*256 {
		return ErrLimit
	}
	return writeStorageJSON(m.state, "storage-remotes.json", remoteJournal{Format: "pb-docs-remotes/v1", States: m.states})
}

func (m *ManagedBackend) pushRemoteLocked(ctx context.Context, remote StorageRemote) (RemoteSyncStatus, error) {
	git, err := m.gitLocked(remote.Name)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	if !remote.Push {
		return RemoteSyncStatus{}, ErrUnsupported
	}
	current, err := m.active.Status(ctx)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	if current.Dirty || current.Head == "" {
		return RemoteSyncStatus{}, fmt.Errorf("%w: commit current documents before pushing", ErrConflict)
	}
	result := m.remoteStatusLocked(remote, current)
	if err := m.verifyRemoteLocked(ctx, remote); err != nil {
		return RemoteSyncStatus{}, err
	}
	if err := git.pushCommit(ctx, current.Head, current.Branch); err != nil {
		result.State, result.Retryable, result.Error = "error", true, "Remote push failed; local commit is retained"
		m.setRemoteStatusLocked(remote, result)
		if persistErr := m.persistRemoteStatesLocked(); persistErr != nil {
			return RemoteSyncStatus{}, persistErr
		}
		if ctx.Err() != nil {
			return RemoteSyncStatus{}, ctx.Err()
		}
		return result, nil
	}
	result.LastPushedHead, result.State, result.Retryable, result.Error = current.Head, "ready", false, ""
	m.setRemoteStatusLocked(remote, result)
	if err := m.persistRemoteStatesLocked(); err != nil {
		return RemoteSyncStatus{}, err
	}
	return result, nil
}

func (m *ManagedBackend) localHistoryBranch() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings.LocalHistoryBranch
}

func sortedStorageRemotes(remotes []StorageRemote) []StorageRemote {
	result := append([]StorageRemote{}, remotes...)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
