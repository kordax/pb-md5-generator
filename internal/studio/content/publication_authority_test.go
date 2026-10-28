package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type publicationAuthorityBackend struct {
	Backend
	head    string
	branch  string
	commits int
	pushes  int
}

func (backend *publicationAuthorityBackend) Status(context.Context) (SyncStatus, error) {
	return SyncStatus{Backend: "git", Branch: backend.branch, Head: backend.head, State: "clean"}, nil
}

func (backend *publicationAuthorityBackend) Commit(context.Context, []string, Actor, string) (string, error) {
	backend.commits++
	return backend.head, nil
}

func (backend *publicationAuthorityBackend) Push(context.Context) error {
	backend.pushes++
	return nil
}

func preparePublicationAuthorityFixture(t *testing.T) (publicationRecoveryFixture, *publicationAuthorityBackend) {
	t.Helper()
	var backend *publicationAuthorityBackend
	fixture := prepareConfiguredPublicationRecoveryFixture(t, func(service *Service, config *Config) {
		backend = &publicationAuthorityBackend{Backend: service.cfg.Storage, head: strings.Repeat("a", 40), branch: "main"}
		service.cfg.Storage, config.Storage = backend, backend
		service.cfg.PushOnPublish, config.PushOnPublish = true, true
	})
	require.Positive(t, backend.commits, "the initial publication must exercise the configured Git contract")
	require.Positive(t, backend.pushes)
	fixture.applySources(t)
	published, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, published)
	require.NoError(t, fixture.transaction.markPublished())
	return fixture, backend
}

func TestOrdinaryPublicationRecoveryRejectsUnrecordedGitHead(t *testing.T) {
	fixture, backend := preparePublicationAuthorityFixture(t)
	commits, pushes := backend.commits, backend.pushes
	backend.head = strings.Repeat("b", 40)
	_, err := fixture.reopen(t)
	require.ErrorIs(t, err, ErrConflict)
	assert.Equal(t, commits, backend.commits, "startup must not create a commit to explain an unknown HEAD")
	assert.Equal(t, pushes, backend.pushes)
	backup, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.original, backup)
	current, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.replacement, current)
	backend.head = strings.Repeat("a", 40)
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), file.Content)
	assert.Equal(t, commits, backend.commits)
	assert.Equal(t, pushes, backend.pushes, "configured automatic pushes must not run during recovery")
}

func TestOrdinaryPublicationRecoveryAcceptsExactDurableOwnCommit(t *testing.T) {
	fixture, backend := preparePublicationAuthorityFixture(t)
	commits, pushes := backend.commits, backend.pushes
	backend.head = strings.Repeat("b", 40)
	require.NoError(t, fixture.transaction.recordCommit(backend.head))
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), file.Content)
	assert.Equal(t, strings.Repeat("b", 40), backend.head)
	assert.Equal(t, commits, backend.commits, "recovery must retain the recorded commit without committing again")
	assert.Equal(t, pushes, backend.pushes)
	_, err = os.Stat(fixture.stage)
	assert.True(t, os.IsNotExist(err), "verified completion must clean its transaction")
}

func TestOrdinaryPublicationRecoveryRejectsCommitReceiptForAnotherHead(t *testing.T) {
	fixture, backend := preparePublicationAuthorityFixture(t)
	backend.head = strings.Repeat("b", 40)
	require.NoError(t, fixture.transaction.recordCommit(backend.head))
	backend.head = strings.Repeat("c", 40)
	_, err := fixture.reopen(t)
	require.ErrorIs(t, err, ErrConflict)
	_, err = os.Stat(fixture.stage)
	assert.NoError(t, err)
	assert.Equal(t, strings.Repeat("c", 40), backend.head, "an unrelated current HEAD must never be rewound")
}

func TestOrdinaryPublicationRecoveryRejectsMalformedPendingReceipt(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*pendingSync)
	}{
		{"missing-hash", func(p *pendingSync) { delete(p.Hashes, p.Paths[0]) }},
		{"changed-hash", func(p *pendingSync) { p.Hashes[p.Paths[0]] = strings.Repeat("e", 64) }},
		{"extra-hash", func(p *pendingSync) { p.Hashes["unexpected.md"] = strings.Repeat("f", 64) }},
		{"different-actor", func(p *pendingSync) { p.Actor.ID = "another-editor" }},
		{"different-message", func(p *pendingSync) { p.Message = "another publication" }},
		{"different-branch", func(p *pendingSync) { p.Branch = "another-branch" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture, backend := preparePublicationAuthorityFixture(t)
			journal := fixture.transaction.journal
			hashes := make(map[string]string, len(journal.Paths))
			for _, name := range journal.Paths {
				hashes[name] = fixture.after.Files[name].Hash
			}
			pending := pendingSync{Revision: journal.After, Paths: append([]string(nil), journal.Paths...), Actor: journal.Actor, Message: journal.Message, Branch: journal.Branch, Hashes: hashes}
			require.NotEmpty(t, pending.Paths)
			test.mutate(&pending)
			require.NoError(t, writeStorageJSON(fixture.config.StateDir, "pending.json", pending))
			before, err := readSource(fixture.config.StateDir, "pending.json", maxStorageJSONBytes)
			require.NoError(t, err)
			commits, pushes := backend.commits, backend.pushes
			_, err = fixture.reopen(t)
			require.ErrorIs(t, err, ErrConflict)
			retained, err := readSource(fixture.config.StateDir, "pending.json", maxStorageJSONBytes)
			require.NoError(t, err)
			assert.Equal(t, before, retained, "an unrecognized receipt must not be overwritten")
			backup, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "index.md"))
			require.NoError(t, err)
			assert.Equal(t, fixture.original, backup)
			assert.Equal(t, commits, backend.commits)
			assert.Equal(t, pushes, backend.pushes)
		})
	}
}

func TestOrdinaryPublicationRecoveryAcceptsMatchingPendingReceipt(t *testing.T) {
	fixture, backend := preparePublicationAuthorityFixture(t)
	journal := fixture.transaction.journal
	hashes := make(map[string]string, len(journal.Paths))
	for _, name := range journal.Paths {
		hashes[name] = fixture.after.Files[name].Hash
	}
	pending := pendingSync{Revision: journal.After, Paths: append([]string(nil), journal.Paths...), Actor: journal.Actor, Message: journal.Message, Branch: journal.Branch, Hashes: hashes}
	require.NoError(t, writeStorageJSON(fixture.config.StateDir, "pending.json", pending))
	commits, pushes := backend.commits, backend.pushes
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), file.Content)
	assert.Equal(t, commits, backend.commits)
	assert.Equal(t, pushes, backend.pushes)
}

func TestOrdinaryPublicationRecoveryRejectsUnexpectedBranch(t *testing.T) {
	fixture, backend := preparePublicationAuthorityFixture(t)
	backend.branch = "another-branch"
	_, err := fixture.reopen(t)
	require.ErrorIs(t, err, ErrConflict)
	assert.Equal(t, "another-branch", backend.branch)
	_, err = os.Stat(fixture.stage)
	assert.NoError(t, err)
}
