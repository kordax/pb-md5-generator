package content

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func readStoredStorage(directory string) (storedStorage, error) {
	data, err := readSource(directory, "storage.json", 128<<10)
	if err != nil {
		return storedStorage{}, err
	}
	var stored storedStorage
	if err := decodeStorageJSON(data, &stored); err != nil {
		return storedStorage{}, err
	}
	if err := validateStoredStorage(stored); err != nil {
		return storedStorage{}, err
	}
	return stored, nil
}

func validateStoredStorage(stored storedStorage) error {
	if stored.Format != "pb-docs-storage/v1" || validateStorageSettings(stored.StorageSettings) != nil || stored.LocalHistoryBranch != "" && !validStorageBranch(stored.LocalHistoryBranch) {
		return ErrInvalid
	}
	revision, err := storageDigest(stored)
	if err != nil || stored.Revision != revision {
		return ErrInvalid
	}
	return nil
}

// Recover is called only after Service owns the managed-location OS locks.
// Neither startup nor configuration recovery contacts any network remote.
func (m *ManagedBackend) Recover(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.recoverTransactionLocked(); err != nil {
		return err
	}
	if err := cleanupStorageOrphans(ctx, m.state); err != nil {
		return err
	}
	m.recoveryFailed, m.warning = false, ""
	stored, err := readStoredStorage(m.state)
	if errors.Is(err, ErrNotFound) {
		enrolled, markerErr := storagePathExists(m.root, ".git/studio-enrollment")
		if markerErr != nil {
			return markerErr
		}
		if enrolled {
			return fmt.Errorf("%w: enrolled Git storage requires its retained storage.json profile; restore the profile before starting", ErrOwnership)
		}
		stored, err = m.initialSettingsLocked(ctx)
		if err != nil {
			return err
		}
		if err := writeStorageJSON(m.state, "storage.json", stored); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := m.activateLocked(stored); err != nil {
		return err
	}
	data, err := readSource(m.state, "storage-remotes.json", maxStorageJSONBytes)
	if errors.Is(err, ErrNotFound) {
		m.states = []storedRemoteStatus{}
		return nil
	}
	if err != nil {
		return err
	}
	var journal remoteJournal
	if decodeStorageJSON(data, &journal) != nil || journal.Format != "pb-docs-remotes/v1" || len(journal.States) > MaxStorageRemotes*256 {
		return ErrInvalid
	}
	for _, state := range journal.States {
		if !validRemoteName(state.Name) || !validStorageBranch(state.Branch) || !validStorageURL(state.URL) || !validRemoteState(state.State) || state.Head != "" && !validGitHead(state.Head) || state.LastPushedHead != "" && !validGitHead(state.LastPushedHead) {
			return ErrInvalid
		}
	}
	m.states = journal.States
	return nil
}

func (m *ManagedBackend) initialSettingsLocked(ctx context.Context) (storedStorage, error) {
	stored := storedStorage{Format: "pb-docs-storage/v1", StorageSettings: StorageSettings{Backend: m.initialBackend, Remotes: []StorageRemote{}}}
	if m.initialBackend == "git" {
		git, err := NewGitBackend(GitConfig{Root: m.root, Remote: m.initialRemote, Timeout: m.timeout})
		if err != nil {
			return storedStorage{}, err
		}
		names, err := git.run(ctx, "remote")
		if err != nil {
			return storedStorage{}, gitError("remote listing", err)
		}
		for _, name := range strings.Fields(string(names)) {
			if len(stored.Remotes) >= MaxStorageRemotes || !validRemoteName(name) {
				return storedStorage{}, ErrLimit
			}
			connection := &GitBackend{root: m.root, remote: name, timeout: m.timeout}
			if err := connection.safeRemote(ctx); err != nil {
				return storedStorage{}, err
			}
			url, err := connection.run(ctx, "remote", "get-url", "--all", name)
			if err != nil {
				return storedStorage{}, gitError("remote configuration", err)
			}
			pushURL, err := connection.run(ctx, "remote", "get-url", "--push", "--all", name)
			if err != nil || strings.TrimSpace(string(url)) != strings.TrimSpace(string(pushURL)) {
				return storedStorage{}, fmt.Errorf("%w: configure one URL per managed remote", ErrInvalid)
			}
			stored.Remotes = append(stored.Remotes, StorageRemote{Name: name, URL: strings.TrimSpace(string(url)), Push: name == m.initialRemote})
			if name == m.initialRemote {
				stored.PullRemote = name
			}
		}
	}
	stored.Remotes = sortedStorageRemotes(stored.Remotes)
	if err := validateStorageSettings(stored.StorageSettings); err != nil {
		return storedStorage{}, err
	}
	var err error
	stored.Revision, err = storageDigest(stored)
	return stored, err
}

func (m *ManagedBackend) activateLocked(stored storedStorage) error {
	active := NewLocalBackend()
	if stored.Backend == "git" {
		remote := stored.PullRemote
		if remote == "" {
			remote = "origin"
		}
		git, err := NewGitBackend(GitConfig{Root: m.root, Remote: remote, Timeout: m.timeout})
		if err != nil {
			return err
		}
		active = git
	}
	m.settings, m.active = stored, active
	return nil
}

func (m *ManagedBackend) recoverTransactionLocked() error {
	data, err := readSource(m.state, "storage-transaction.json", 1<<20)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var transaction storageTransaction
	if decodeStorageJSON(data, &transaction) != nil || transaction.Format != "pb-docs-storage-transaction/v1" || !validID(transaction.ID) || validateStoredStorage(transaction.Old) != nil || validateStoredStorage(transaction.New) != nil || len(transaction.NewGitConfig) > 128<<10 || len(transaction.OldGitConfig) > 128<<10 {
		return ErrOwnership
	}
	if transaction.New.Backend != "git" || transaction.Enrolling != (transaction.Old.Backend == "local") || transaction.Enrolling && transaction.New.LocalHistoryBranch == "" || !transaction.Enrolling && (transaction.Old.Backend != "git" || transaction.New.LocalHistoryBranch != transaction.Old.LocalHistoryBranch || len(transaction.OldGitConfig) == 0) || len(transaction.NewGitConfig) == 0 {
		return ErrOwnership
	}
	current, err := readStoredStorage(m.state)
	if err != nil {
		return err
	}
	committed := current.Revision == transaction.New.Revision
	if !committed && current.Revision != transaction.Old.Revision {
		return fmt.Errorf("%w: storage recovery found changed settings; transaction retained", ErrConflict)
	}
	if transaction.Enrolling {
		if err := m.recoverEnrollmentLocked(transaction, committed); err != nil {
			return err
		}
	} else {
		currentConfig, err := readSource(m.root, ".git/config", 128<<10)
		if err != nil || !bytes.Equal(currentConfig, transaction.OldGitConfig) && !bytes.Equal(currentConfig, transaction.NewGitConfig) {
			return fmt.Errorf("%w: storage recovery found changed Git configuration; transaction retained", ErrConflict)
		}
		wanted := transaction.OldGitConfig
		if committed {
			wanted = transaction.NewGitConfig
		}
		if err := writeStorageBytes(m.root, ".git/config", wanted); err != nil {
			return err
		}
	}
	return m.cleanupTransactionLocked(transaction)
}

func (m *ManagedBackend) recoverEnrollmentLocked(transaction storageTransaction, committed bool) error {
	installed, err := storagePathExists(m.root, ".git")
	if err != nil {
		return err
	}
	if installed {
		marker, err := readSource(m.root, ".git/studio-enrollment", 64)
		config, configErr := readSource(m.root, ".git/config", 128<<10)
		if err != nil || configErr != nil || string(marker) != transaction.ID || !bytes.Equal(config, transaction.NewGitConfig) {
			return fmt.Errorf("%w: storage recovery cannot adopt changed Git metadata; transaction retained", ErrOwnership)
		}
	} else if committed {
		source := filepath.Join(m.state, ".txn-storage-"+transaction.ID, "source")
		marker, markerErr := readSource(source, ".git/studio-enrollment", 64)
		config, configErr := readSource(source, ".git/config", 128<<10)
		if markerErr != nil || configErr != nil || string(marker) != transaction.ID || !bytes.Equal(config, transaction.NewGitConfig) {
			return fmt.Errorf("%w: committed Git enrollment has changed staged metadata; transaction retained", ErrOwnership)
		}
		stage := filepath.Join(source, ".git")
		if err := os.Rename(stage, filepath.Join(m.root, ".git")); err != nil {
			return fmt.Errorf("%w: committed Git enrollment needs its retained transaction", ErrOwnership)
		}
		if err := syncStorageDirectory(source, "."); err != nil {
			return err
		}
		if err := syncStorageDirectory(m.root, "."); err != nil {
			return err
		}
	}
	if installed && !committed {
		return removeStorageTree(m.root, ".git")
	}
	return nil
}

func (m *ManagedBackend) cleanupTransactionLocked(transaction storageTransaction) error {
	if err := removeStorageTree(m.state, ".txn-storage-"+transaction.ID); err != nil {
		return err
	}
	return removeStorageFile(m.state, "storage-transaction.json")
}

func (m *ManagedBackend) configure(ctx context.Context, input StorageUpdate, actor Actor, source string, before sourceTree) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || input.Revision != m.settings.Revision {
		return ErrConflict
	}
	persisted, err := readStoredStorage(m.state)
	if err != nil || persisted.Revision != m.settings.Revision {
		return errors.Join(ErrConflict, err)
	}
	if m.settings.Backend == "git" && input.Backend == "local" {
		return fmt.Errorf("%w: Git-to-local migration is not available; Git content and drafts are retained", ErrUnsupported)
	}
	if err := m.recoverTransactionLocked(); err != nil {
		return err
	}
	m.warning = ""
	settings := storedStorage{Format: "pb-docs-storage/v1", StorageSettings: StorageSettings{Backend: input.Backend, PullRemote: input.PullRemote, Remotes: sortedStorageRemotes(input.Remotes)}, LocalHistoryBranch: m.settings.LocalHistoryBranch}
	if err := validateStorageSettings(settings.StorageSettings); err != nil {
		return err
	}
	enrolling := m.settings.Backend == "local" && settings.Backend == "git"
	if !enrolling && input.InitialBranch != "" {
		return ErrInvalid
	}
	if enrolling {
		if input.InitialBranch == "" {
			input.InitialBranch = "main"
		}
		if !validStorageBranch(input.InitialBranch) {
			return ErrInvalid
		}
		settings.LocalHistoryBranch = input.InitialBranch
	}
	settings.Revision, err = storageDigest(settings)
	if err != nil {
		return err
	}
	if settings.Revision == m.settings.Revision {
		return nil
	}
	if settings.Backend == "local" {
		return ErrInvalid
	}
	stage, id, err := createStorageStage(m.state, ".txn-storage-")
	if err != nil {
		return err
	}
	transaction := storageTransaction{Format: "pb-docs-storage-transaction/v1", ID: id, Old: m.settings, New: settings, Enrolling: enrolling}
	journaled := false
	defer func() {
		if !journaled {
			_ = removeStorageTree(m.state, ".txn-storage-"+id)
		}
	}()
	if err := m.prepareStorageLocked(ctx, &transaction, stage, source, before, actor, input.InitialBranch); err != nil {
		return err
	}
	current, err := m.active.Status(ctx)
	if err != nil {
		return err
	}
	fresh, err := scanTree(ctx, m.root, storageIdentity(current), "")
	if err != nil || fresh.Revision != before.Revision {
		return errors.Join(ErrConflict, err)
	}
	if !enrolling {
		actual, err := readSource(m.root, ".git/config", 128<<10)
		if err != nil || !bytes.Equal(actual, transaction.OldGitConfig) {
			return errors.Join(ErrConflict, err)
		}
	}
	if err := writeStorageJSON(m.state, "storage-transaction.json", transaction); err != nil {
		return err
	}
	journaled = true
	rollback := func(cause error) error {
		recoveryErr := m.recoverTransactionLocked()
		m.recoveryFailed = recoveryErr != nil
		return errors.Join(cause, recoveryErr)
	}
	if enrolling {
		if exists, err := storagePathExists(m.root, ".git"); err != nil || exists {
			return rollback(errors.Join(ErrConflict, err))
		}
		if err := os.Rename(filepath.Join(stage, "source", ".git"), filepath.Join(m.root, ".git")); err != nil {
			return rollback(err)
		}
		if err := syncStorageDirectory(filepath.Join(stage, "source"), "."); err != nil {
			return rollback(err)
		}
		if err := syncStorageDirectory(m.root, "."); err != nil {
			return rollback(err)
		}
	} else {
		actual, err := readSource(m.root, ".git/config", 128<<10)
		if err != nil || !bytes.Equal(actual, transaction.OldGitConfig) {
			return rollback(errors.Join(ErrConflict, err))
		}
		if err := writeStorageBytes(m.root, ".git/config", transaction.NewGitConfig); err != nil {
			return rollback(err)
		}
	}
	commitErr := writeStorageJSON(m.state, "storage.json", settings)
	if commitErr != nil {
		current, readErr := readStoredStorage(m.state)
		if readErr != nil || current.Revision != settings.Revision {
			return rollback(errors.Join(commitErr, readErr))
		}
	}
	if err := m.activateLocked(settings); err != nil {
		m.recoveryFailed = true
		return err
	}
	m.reconcileRemoteStatesLocked()
	if err := m.persistRemoteStatesLocked(); err != nil {
		m.warning = "Storage settings are active; cached connection state needs attention"
	}
	m.recoveryFailed = false
	if commitErr != nil {
		m.warning = "Storage settings are active; durable transaction recovery is retained"
		return nil
	}
	if err := m.cleanupTransactionLocked(transaction); err != nil {
		m.warning = "Storage settings are active; transaction cleanup needs attention"
		// The persisted settings are the commit point. Preserve the journal so
		// restart can finish cleanup without reversing a completed enrollment.
		return nil
	}
	return nil
}

func (m *ManagedBackend) prepareStorageLocked(ctx context.Context, transaction *storageTransaction, stage, source string, before sourceTree, actor Actor, branch string) error {
	work := filepath.Join(stage, "source")
	if err := os.Mkdir(work, 0o700); err != nil {
		return err
	}
	git := &GitBackend{root: work, remote: "origin", timeout: m.timeout}
	initialBranch := "storage-preparation"
	if transaction.Enrolling {
		initialBranch = branch
		if exists, err := storagePathExists(m.root, ".git"); err != nil || exists {
			return errors.Join(ErrConflict, err)
		}
		if _, err := scanTree(ctx, source, "", work); err != nil {
			return err
		}
	}
	if _, err := git.run(ctx, "check-ref-format", "--branch", initialBranch); err != nil {
		return ErrInvalid
	}
	if _, err := git.run(ctx, "init", "--quiet", "--template=", "--initial-branch="+initialBranch, "."); err != nil {
		return gitError("initialization", err)
	}
	if transaction.Enrolling {
		if _, err := git.Commit(ctx, sortedFiles(before), actor, "docs: enroll managed documentation"); err != nil {
			return err
		}
		if err := writeStorageBytes(work, ".git/studio-enrollment", []byte(transaction.ID)); err != nil {
			return err
		}
	} else {
		var err error
		transaction.OldGitConfig, err = readSource(m.root, ".git/config", 128<<10)
		if err != nil {
			return err
		}
		if err := writeStorageBytes(work, ".git/config", transaction.OldGitConfig); err != nil {
			return err
		}
	}
	if err := git.configureRemotes(ctx, transaction.Old.Remotes, transaction.New.Remotes); err != nil {
		return err
	}
	var err error
	transaction.NewGitConfig, err = readSource(work, ".git/config", 128<<10)
	if err != nil {
		return err
	}
	return syncStorageTree(ctx, filepath.Join(work, ".git"))
}
