package content

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

func validStorageURL(value string) bool {
	return utf8.ValidString(value) && len(value) <= 2048 && !strings.ContainsAny(value, "?#") && allowedRemote(value)
}

func validGitHead(head string) bool {
	if len(head) != 40 && len(head) != 64 {
		return false
	}
	_, err := hexDecode(head)
	return err == nil
}

func validRemoteState(state string) bool {
	return state == "unknown" || state == "ready" || state == "pending" || state == "error" || state == "disabled"
}

// The additional protocol is deliberately finite. It cannot select templates,
// hooks, executables, arbitrary config keys, force pushes or a shell fragment.
func (g *GitBackend) validStorageCommand(input string, arguments []string) bool {
	if input != "" || len(arguments) == 0 {
		return false
	}
	switch arguments[0] {
	case "init":
		return len(arguments) == 5 && arguments[1] == "--quiet" && arguments[2] == "--template=" && strings.HasPrefix(arguments[3], "--initial-branch=") && validStorageBranch(strings.TrimPrefix(arguments[3], "--initial-branch=")) && arguments[4] == "."
	case "remote":
		if len(arguments) == 1 {
			return true
		}
		if len(arguments) == 3 && arguments[1] == "remove" && arguments[2] == g.remote && validRemoteName(g.remote) {
			return true
		}
		return len(arguments) == 4 && arguments[1] == "add" && arguments[2] == g.remote && validRemoteName(g.remote) && validStorageURL(arguments[3])
	case "ls-remote":
		return len(arguments) == 4 && arguments[1] == "--refs" && arguments[2] == "--" && arguments[3] == g.remote && validRemoteName(g.remote)
	case "push":
		if len(arguments) != 4 || arguments[1] != "--" || arguments[2] != g.remote || !validRemoteName(g.remote) {
			return false
		}
		head, branch, ok := strings.Cut(arguments[3], ":refs/heads/")
		return ok && validGitHead(head) && validStorageBranch(branch)
	case "rev-list":
		if len(arguments) != 4 || arguments[1] != "--left-right" || arguments[2] != "--count" {
			return false
		}
		prefix := "HEAD...refs/remotes/" + g.remote + "/"
		return validRemoteName(g.remote) && strings.HasPrefix(arguments[3], prefix) && validStorageBranch(strings.TrimPrefix(arguments[3], prefix))
	default:
		return false
	}
}

func (g *GitBackend) configureRemotes(ctx context.Context, old, next []StorageRemote) error {
	if err := g.safeConfig(ctx); err != nil {
		return err
	}
	data, err := g.run(ctx, "remote")
	if err != nil {
		return gitError("remote listing", err)
	}
	existing := make(map[string]bool)
	for _, name := range strings.Fields(string(data)) {
		existing[name] = true
	}
	remove := make(map[string]bool)
	for _, remote := range old {
		remove[remote.Name] = true
	}
	for _, remote := range next {
		remove[remote.Name] = true
	}
	for name := range remove {
		if !existing[name] {
			continue
		}
		connection := *g
		connection.remote = name
		if _, err := connection.run(ctx, "remote", "remove", name); err != nil {
			return gitError("remote configuration", err)
		}
	}
	for _, remote := range next {
		connection := *g
		connection.remote = remote.Name
		if _, err := connection.run(ctx, "remote", "add", remote.Name, remote.URL); err != nil {
			return gitError("remote configuration", err)
		}
		if err := connection.safeRemote(ctx); err != nil {
			return err
		}
		configured, err := connection.run(ctx, "remote", "get-url", "--all", remote.Name)
		if err != nil || strings.TrimSpace(string(configured)) != remote.URL {
			return fmt.Errorf("%w: managed remote URL must not be rewritten by Git configuration", ErrConflict)
		}
	}
	return nil
}

func (g *GitBackend) pushCommit(ctx context.Context, head, branch string) error {
	if !validGitHead(head) || !validStorageBranch(branch) {
		return ErrInvalid
	}
	if err := g.safeRemote(ctx); err != nil {
		return err
	}
	if err := g.clean(ctx); err != nil {
		return err
	}
	if _, err := g.run(ctx, "push", "--", g.remote, head+":refs/heads/"+branch); err != nil {
		return gitError("push", err)
	}
	return nil
}

func (m *ManagedBackend) checkRemote(ctx context.Context, name, revision string) (RemoteSyncStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if revision != m.settings.Revision {
		return RemoteSyncStatus{}, ErrConflict
	}
	remote, err := m.remoteLocked(name)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	git, err := m.gitLocked(name)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	current, err := m.active.Status(ctx)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	result := m.remoteStatusLocked(remote, current)
	if err := m.verifyRemoteLocked(ctx, remote); err != nil {
		return RemoteSyncStatus{}, err
	}
	if _, err := git.run(ctx, "ls-remote", "--refs", "--", remote.Name); err != nil {
		result.State, result.Retryable, result.Error = "error", true, "Remote connection failed; verify the connection and private SSH configuration"
		m.setRemoteStatusLocked(remote, result)
		if persistErr := m.persistRemoteStatesLocked(); persistErr != nil {
			return RemoteSyncStatus{}, persistErr
		}
		if ctx.Err() != nil {
			return RemoteSyncStatus{}, ctx.Err()
		}
		return result, nil
	}
	result.State, result.Retryable, result.Error = "ready", false, ""
	m.setRemoteStatusLocked(remote, result)
	if err := m.persistRemoteStatesLocked(); err != nil {
		return RemoteSyncStatus{}, err
	}
	return result, nil
}

func (m *ManagedBackend) pushRemote(ctx context.Context, name, revision string) (RemoteSyncStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if revision != m.settings.Revision {
		return RemoteSyncStatus{}, ErrConflict
	}
	remote, err := m.remoteLocked(name)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	return m.pushRemoteLocked(ctx, remote)
}

func (s *Service) historyBranchMatches(previous, current string) bool {
	if previous == current {
		return true
	}
	managed, ok := s.cfg.Storage.(*ManagedBackend)
	return ok && previous == "" && current != "" && managed.localHistoryBranch() == current
}

func (m *ManagedBackend) verifyRemoteLocked(ctx context.Context, remote StorageRemote) error {
	git, err := m.gitLocked(remote.Name)
	if err != nil {
		return err
	}
	if err := git.safeRemote(ctx); err != nil {
		return err
	}
	for _, arguments := range [][]string{{"remote", "get-url", "--all", remote.Name}, {"remote", "get-url", "--push", "--all", remote.Name}} {
		data, err := git.run(ctx, arguments...)
		if err != nil || strings.TrimSpace(string(data)) != remote.URL {
			return fmt.Errorf("%w: Git remote configuration changed outside the CMS", ErrConflict)
		}
	}
	return nil
}

func storageConfigurationConflict() error {
	return fmt.Errorf("%w: retry the pending local commit before changing storage settings; documents and drafts are retained", ErrConflict)
}
