package content

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRejectedDraftBoundariesLeaveSourcesAndDraftInventoryUntouched(t *testing.T) {
	service, _ := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	before, err := service.Read(t.Context(), project.Documents[0].Path)
	require.NoError(t, err)
	text := "draft source"
	binary := bytes.Repeat([]byte{0xff}, MaxBinaryBytes/2+1)
	cases := []struct {
		name  string
		input DraftInput
		actor Actor
		err   error
	}{
		{"missing actor", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text}}, Actor{}, ErrInvalid},
		{"long actor ID", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text}}, Actor{ID: strings.Repeat("a", 129)}, ErrInvalid},
		{"long actor name", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text}}, Actor{ID: "actor", Name: strings.Repeat("a", 257)}, ErrInvalid},
		{"long actor email", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text}}, Actor{ID: "actor", Email: strings.Repeat("a", 321)}, ErrInvalid},
		{"control actor", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text}}, Actor{ID: "actor", Name: "bad\nname"}, ErrInvalid},
		{"invalid UTF8 actor", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text}}, Actor{ID: "actor", Email: string([]byte{0xff})}, ErrInvalid},
		{"bad revision", DraftInput{BaseRevision: strings.Repeat("g", 64), Change: Change{Operation: "write", Path: "new.md", Content: &text}}, editor, ErrInvalid},
		{"both change forms", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "mkdir", Path: "one"}, Changes: []Change{{Operation: "mkdir", Path: "two"}}}, editor, ErrInvalid},
		{"both payload forms", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text, Data: []byte("other")}}, editor, ErrInvalid},
		{"invalid text encoding", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Data: []byte{0xff}}}, editor, ErrInvalid},
		{"payload on removal", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "delete", Path: "new.md", Content: &text}}, editor, ErrInvalid},
		{"move into itself", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "move", Path: "folder", To: "folder/nested"}}, editor, ErrInvalid},
		{"manifest removal", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "delete", Path: "docs.json"}}, editor, ErrInvalid},
		{"manifest destination", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "move", Path: "new.md", To: "docs.json"}}, editor, ErrInvalid},
		{"unknown change", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "execute", Path: "new.md"}}, editor, ErrInvalid},
		{"unknown format", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text, Kind: "xml"}}, editor, ErrInvalid},
		{"invalid document ID", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text, DocumentID: "bad/id"}}, editor, ErrInvalid},
		{"nonHTML output", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text, OutputPath: "output.txt"}}, editor, ErrInvalid},
		{"internal output", DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", Content: &text, OutputPath: "_docs/output.html"}}, editor, ErrInvalid},
		{"aggregate payload cap", DraftInput{BaseRevision: project.Revision, Changes: []Change{{Operation: "write", Path: "one.pdf", Data: binary}, {Operation: "write", Path: "two.pdf", Data: binary}}}, editor, ErrLimit},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, err := service.SaveDraft(t.Context(), item.input, item.actor)
			assert.ErrorIs(t, err, item.err)
		})
	}
	drafts, err := service.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
	after, err := service.Read(t.Context(), before.Path)
	require.NoError(t, err)
	assert.Equal(t, before.Content, after.Content)
	assert.Equal(t, project.Revision, after.Revision)
	minimal, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "minimal.txt", Content: &text}}, Actor{ID: "minimal"})
	require.NoError(t, err)
	assert.Equal(t, "minimal", minimal.Actor.Name)
	assert.Equal(t, "editor@localhost", minimal.Actor.Email)
}

func TestDraftCountAndBatchLimitsRecoverAfterDiscard(t *testing.T) {
	service, _ := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	text := "small draft"
	changes := make([]Change, MaxChanges+1)
	for i := range changes {
		changes[i] = Change{Operation: "write", Path: fmt.Sprintf("batch/%03d.txt", i), Content: &text}
	}
	_, err = service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Changes: changes}, editor)
	assert.ErrorIs(t, err, ErrLimit)
	batch, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Changes: changes[:MaxChanges]}, editor)
	require.NoError(t, err)
	assert.Len(t, batch.Changes, MaxChanges)
	require.NoError(t, service.DiscardDraft(t.Context(), batch.ID, batch.Revision, editor))
	var first Draft
	for i := range MaxDrafts {
		draft, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: fmt.Sprintf("drafts/%03d.txt", i), Content: &text}}, editor)
		require.NoError(t, err)
		if i == 0 {
			first = draft
		}
	}
	_, err = service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "overflow.txt", Content: &text}}, editor)
	assert.ErrorIs(t, err, ErrLimit)
	require.NoError(t, service.DiscardDraft(t.Context(), first.ID, first.Revision, editor))
	_, err = service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "replacement.txt", Content: &text}}, editor)
	require.NoError(t, err)
	drafts, err := service.Drafts(t.Context())
	require.NoError(t, err)
	assert.Len(t, drafts, MaxDrafts)
	after, err := service.Project(t.Context())
	require.NoError(t, err)
	assert.Equal(t, project.Revision, after.Revision)
}

func TestStateByteQuotaRejectsDraftWithoutDeletingExistingState(t *testing.T) {
	service, config := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	allocation := filepath.Join(config.StateDir, "external-allocation.bin")
	file, err := os.OpenFile(allocation, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(MaxStateBytes))
	require.NoError(t, file.Close())
	text := "draft after quota is released"
	input := DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "quota.txt", Content: &text}}
	_, err = service.SaveDraft(t.Context(), input, editor)
	assert.ErrorIs(t, err, ErrLimit)
	drafts, err := service.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
	info, err := os.Stat(allocation)
	require.NoError(t, err)
	assert.Equal(t, int64(MaxStateBytes), info.Size())
	require.NoError(t, os.Remove(allocation))
	_, err = service.SaveDraft(t.Context(), input, editor)
	assert.NoError(t, err)
}

func TestSourceReadsDetectSameStampChangesAndRejectInvalidText(t *testing.T) {
	service, config := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	primary := project.Documents[0].Path
	before, err := service.Read(t.Context(), primary)
	require.NoError(t, err)
	name := filepath.Join(config.Root, filepath.FromSlash(primary))
	info, err := os.Stat(name)
	require.NoError(t, err)
	external := strings.Repeat("x", len(before.Content))
	require.NotEqual(t, before.Content, external)
	require.NoError(t, os.WriteFile(name, []byte(external), 0o600))
	require.NoError(t, os.Chtimes(name, info.ModTime(), info.ModTime()))
	changed, err := service.Read(t.Context(), primary)
	require.NoError(t, err)
	assert.Equal(t, external, changed.Content)
	assert.NotEqual(t, before.Revision, changed.Revision)
	mine := "# Stale editor source"
	draft, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: before.Revision, Change: Change{Operation: "write", Path: primary, Content: &mine}}, editor)
	require.NoError(t, err)
	_, err = service.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	require.NoError(t, os.WriteFile(name, []byte{0xff, 0xfe}, 0o600))
	_, err = service.Read(t.Context(), primary)
	assert.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, os.WriteFile(name, []byte(external), 0o600))
	_, err = service.Read(t.Context(), "missing.md")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = service.Read(t.Context(), "../outside.md")
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestConstructorInvalidConfigurationsDoNotAdoptExistingPaths(t *testing.T) {
	service, config := managedFixture(t)
	require.NoError(t, service.Close())
	cases := []struct {
		name   string
		modify func(*Config)
	}{
		{"missing root", func(config *Config) { config.Root = "" }},
		{"missing state", func(config *Config) { config.StateDir = "" }},
		{"missing output", func(config *Config) { config.OutputDir = "" }},
		{"missing compiler", func(config *Config) { config.Compile = nil }},
		{"short timeout", func(config *Config) { config.OperationTimeout = time.Millisecond }},
		{"long timeout", func(config *Config) { config.OperationTimeout = 2*time.Minute + time.Second }},
		{"overlapping state", func(config *Config) { config.StateDir = config.Root }},
		{"nested output", func(config *Config) { config.OutputDir = filepath.Join(config.Root, "generated") }},
		{"file parent", func(config *Config) { config.Root = filepath.Join(config.Root, "index.md", "nested") }},
		{"invalid filesystem path", func(config *Config) { config.Root += "\x00" }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			candidate := config
			item.modify(&candidate)
			opened, err := New(candidate)
			assert.Error(t, err)
			if opened != nil {
				assert.NoError(t, opened.Close())
			}
		})
	}
	reopened, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	_, err = reopened.Project(t.Context())
	assert.NoError(t, err)
}

func TestLocalStorageCapabilitiesNeverDiscardDrafts(t *testing.T) {
	service, _ := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	primary := project.Documents[0].Path
	draft := saveText(t, service, primary, "# Retained local draft")
	branches, err := service.Branches(t.Context())
	require.NoError(t, err)
	assert.Empty(t, branches)
	operations := []func() error{
		func() error { _, err := service.CreateBranch(t.Context(), "feature", project.Revision); return err },
		func() error { _, err := service.Checkout(t.Context(), "main", project.Revision); return err },
		func() error { _, err := service.Fetch(t.Context(), project.Revision); return err },
		func() error { _, err := service.Pull(t.Context(), project.Revision); return err },
		func() error { _, err := service.Push(t.Context(), project.Revision); return err },
	}
	for _, operation := range operations {
		assert.ErrorIs(t, operation(), ErrUnsupported)
	}
	assert.ErrorIs(t, NewLocalBackend().RestoreHead(t.Context(), strings.Repeat("a", 40)), ErrUnsupported)
	retained, err := service.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft.Revision, retained.Revision)
	assert.Equal(t, "# Retained local draft", *retained.Change.Content)
	after, err := service.Project(t.Context())
	require.NoError(t, err)
	assert.Equal(t, project.Revision, after.Revision)
}

func TestAlreadyCanceledDraftOperationsDoNotConsumeSavedDraft(t *testing.T) {
	service, _ := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	draft := saveText(t, service, project.Documents[0].Path, "# Keep this draft")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	text := "# This draft must not be created"
	for range 32 {
		_, err := service.Drafts(ctx)
		require.ErrorIs(t, err, context.Canceled)
		_, err = service.ReadDraft(ctx, draft.ID)
		require.ErrorIs(t, err, context.Canceled)
		_, err = service.SaveDraft(ctx, DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "canceled.md", Content: &text}}, editor)
		require.ErrorIs(t, err, context.Canceled)
	}
	assert.ErrorIs(t, service.DiscardDraft(ctx, draft.ID, draft.Revision, editor), context.Canceled)
	_, err = service.ReadDraft(t.Context(), draft.ID)
	assert.NoError(t, err)
	drafts, err := service.Drafts(t.Context())
	require.NoError(t, err)
	assert.Len(t, drafts, 1)
}

func TestFailedFilesystemChangesPreservePublishedSiteAndRecoverableDraft(t *testing.T) {
	service, config := managedFixture(t)
	publishDraft(t, service, saveText(t, service, "folder/child.txt", "attachment remains"))
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	primary := project.Documents[0].Path
	before, err := service.Read(t.Context(), primary)
	require.NoError(t, err)
	siteBefore, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	text := "unpublished attempted content"
	invalidManifest := "{"
	cases := []struct {
		name   string
		change Change
		err    error
	}{
		{"missing removal", Change{Operation: "delete", Path: "missing.md"}, ErrNotFound},
		{"missing move", Change{Operation: "move", Path: "missing.md", To: "moved.md"}, ErrNotFound},
		{"existing destination", Change{Operation: "move", Path: "folder/child.txt", To: primary}, ErrConflict},
		{"folder as file removal", Change{Operation: "delete", Path: "folder"}, ErrInvalid},
		{"file as folder removal", Change{Operation: "rmdir", Path: "folder/child.txt"}, ErrInvalid},
		{"missing folder removal", Change{Operation: "rmdir", Path: "missing-folder"}, ErrNotFound},
		{"nonempty folder removal", Change{Operation: "rmdir", Path: "folder"}, ErrInvalid},
		{"unsupported move extension", Change{Operation: "move", Path: "folder/child.txt", To: "folder/file.exe"}, ErrInvalid},
		{"file used as parent", Change{Operation: "write", Path: primary + "/nested.md", Content: &text}, nil},
		{"file used as folder", Change{Operation: "mkdir", Path: primary}, nil},
		{"invalid manifest", Change{Operation: "write", Path: "docs.json", Content: &invalidManifest}, ErrInvalid},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			draft, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: item.change}, editor)
			require.NoError(t, err)
			_, err = service.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
			if item.err != nil {
				assert.ErrorIs(t, err, item.err)
			} else {
				assert.Error(t, err)
			}
			retained, err := service.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, err)
			assert.Equal(t, draft.Revision, retained.Revision)
			after, err := service.Read(t.Context(), primary)
			require.NoError(t, err)
			assert.Equal(t, before.Content, after.Content)
			assert.Equal(t, project.Revision, after.Revision)
			siteAfter, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, siteBefore, siteAfter)
			require.NoError(t, service.DiscardDraft(t.Context(), draft.ID, draft.Revision, editor))
		})
	}
}

func TestBuildCurrentRefusesMalformedOwnershipInventoryWithoutReplacingFiles(t *testing.T) {
	service, config := managedFixture(t)
	_, err := service.BuildCurrent(t.Context(), editor)
	require.NoError(t, err)
	index := filepath.Join(config.OutputDir, "index.html")
	before, err := os.ReadFile(index)
	require.NoError(t, err)
	inventory := filepath.Join(config.OutputDir, "_docs", "build.json")
	original, err := os.ReadFile(inventory)
	require.NoError(t, err)
	for _, contents := range []string{
		"{",
		`{"format":"not-a-compiler-site","files":[]}`,
		`{"format":"pb-docs-site/v1","files":["../outside"]}`,
		`{"format":"pb-docs-site/v1","files":["."]}`,
	} {
		require.NoError(t, os.WriteFile(inventory, []byte(contents), 0o600))
		_, err := service.BuildCurrent(t.Context(), editor)
		assert.ErrorIs(t, err, ErrOwnership)
		after, err := os.ReadFile(index)
		require.NoError(t, err)
		assert.Equal(t, before, after)
	}
	require.NoError(t, os.WriteFile(inventory, original, 0o600))
	_, err = service.BuildCurrent(t.Context(), editor)
	assert.NoError(t, err)
}

func TestUnavailablePrivateStateDoesNotLoseDraftWhenDirectoryReturns(t *testing.T) {
	service, config := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	draft := saveText(t, service, project.Documents[0].Path, "# Saved across state outage")
	backup := config.StateDir + "-unavailable"
	if err := os.Rename(config.StateDir, backup); err != nil {
		if runtime.GOOS == "windows" {
			retained, readErr := service.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, readErr)
			assert.Equal(t, draft.Revision, retained.Revision, "exclusive Windows handles may prevent the directory outage itself")
			return
		}
		require.NoError(t, err)
	}
	_, err = service.ReadDraft(t.Context(), draft.ID)
	assert.Error(t, err)
	assert.Error(t, service.DiscardDraft(t.Context(), draft.ID, draft.Revision, editor))
	_, err = service.SaveDraft(t.Context(), DraftInput{ID: draft.ID, Revision: draft.Revision, BaseRevision: draft.BaseRevision, Change: draft.Change}, editor)
	assert.Error(t, err)
	current, err := service.Read(t.Context(), project.Documents[0].Path)
	require.NoError(t, err)
	assert.Equal(t, project.Revision, current.Revision)
	require.NoError(t, os.Rename(backup, config.StateDir))
	retained, err := service.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft.Revision, retained.Revision)
	assert.Equal(t, "# Saved across state outage", *retained.Change.Content)
}

func TestGitTransportPolicyRejectsMalformedAndAmbiguousRemoteLocations(t *testing.T) {
	for _, item := range []struct {
		name    string
		remote  string
		allowed bool
	}{
		{"absolute local path", filepath.Join(t.TempDir(), "remote.git"), true},
		{"SSH URL", "ssh://git@example.test/team/repository.git", true},
		{"SSH custom port", "ssh://example.test:2222/team/repository.git", true},
		{"SCP remote", "git@example.test:team/repository.git", true},
		{"SCP path containing URI delimiter", "git@example.test:repo://snapshot", true},
		{"HTTP transport", "http://example.test/team/repository.git", false},
		{"HTTPS transport", "https://example.test/team/repository.git", false},
		{"unsupported scheme", "ext://example.test/repository.git", false},
		{"remote file host", "file://example.test/repository.git", false},
		{"file URL query", "file:///absolute/repository.git?query=1", false},
		{"file URL fragment", "file:///absolute/repository.git#fragment", false},
		{"SSH URL query", "ssh://git@example.test/team/repository.git?query=1", false},
		{"SSH URL fragment", "ssh://git@example.test/team/repository.git#fragment", false},
		{"SSH leading dash host", "ssh://-host/repository.git", false},
		{"relative local path", "repository.git", false},
		{"ambiguous host colon path", "host:repository.git", false},
		{"leading option", "--upload-pack=unexpected", false},
		{"control character", "git@example.test:repo\npath", false},
		{"malformed percent escape", "https://host/%zz", false},
		{"malformed SSH port", "ssh://host:bad/path", false},
	} {
		t.Run(item.name, func(t *testing.T) {
			assert.Equal(t, item.allowed, allowedRemote(item.remote))
		})
	}
}

func TestDamagedDraftFilesFailClosedAndRecoverAfterRepair(t *testing.T) {
	service, config := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	name := project.Documents[0].Path
	before, err := service.Read(t.Context(), name)
	require.NoError(t, err)
	draft := saveText(t, service, name, "# Recover this exact draft")
	_, err = service.ReadDraft(t.Context(), "invalid-id")
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = service.ReadDraft(t.Context(), strings.Repeat("0", 32))
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, service.DiscardDraft(t.Context(), strings.Repeat("0", 32), draft.Revision, editor), ErrNotFound)
	metadata := filepath.Join(config.StateDir, "drafts", draft.ID+".meta.json")
	originalMetadata, err := os.ReadFile(metadata)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(metadata, []byte("{"), 0o600))
	_, err = service.ReadDraft(t.Context(), draft.ID)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = service.Drafts(t.Context())
	assert.ErrorIs(t, err, ErrInvalid)
	assert.ErrorIs(t, service.DiscardDraft(t.Context(), draft.ID, draft.Revision, editor), ErrInvalid)
	require.NoError(t, os.WriteFile(metadata, originalMetadata, 0o600))
	payload := filepath.Join(config.StateDir, "drafts", draft.ID+"-"+draft.Revision+".json")
	originalPayload, err := os.ReadFile(payload)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(payload, []byte("{"), 0o600))
	_, err = service.ReadDraft(t.Context(), draft.ID)
	assert.ErrorIs(t, err, ErrInvalid)
	drafts, err := service.Drafts(t.Context())
	require.NoError(t, err)
	assert.Len(t, drafts, 1, "metadata remains available when payload recovery is needed")
	require.NoError(t, os.Remove(payload))
	require.NoError(t, os.Mkdir(payload, 0o700))
	_, err = service.ReadDraft(t.Context(), draft.ID)
	assert.Error(t, err)
	require.NoError(t, os.Remove(payload))
	require.NoError(t, os.WriteFile(payload, originalPayload, 0o600))
	retained, err := service.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
	require.NotNil(t, retained.Change.Content)
	assert.Equal(t, "# Recover this exact draft", *retained.Change.Content)
	current, err := service.Read(t.Context(), name)
	require.NoError(t, err)
	assert.Equal(t, before.Content, current.Content)
}

type boundaryCommitFailure struct {
	Backend
	fail bool
}

func (b *boundaryCommitFailure) Commit(ctx context.Context, paths []string, actor Actor, message string) (string, error) {
	if b.fail {
		return "", errors.New("local test commit unavailable")
	}
	return b.Backend.Commit(ctx, paths, actor, message)
}

func TestPublicSynchronizationStatusExposesAndRecoversPendingCommit(t *testing.T) {
	original, config, _ := gitManagedFixture(t)
	require.NoError(t, original.Close())
	require.NotNil(t, config.Storage)
	backend := &boundaryCommitFailure{Backend: config.Storage, fail: true}
	config.Storage = backend
	service, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, service.Close()) })
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	name := project.Documents[0].Path
	draft := saveText(t, service, name, "# Retained while commit is unavailable")
	result, err := service.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	require.NoError(t, err)
	assert.True(t, result.Published)
	assert.True(t, result.Sync.Retryable)
	status, err := service.Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "git", status.Backend)
	assert.Equal(t, "main", status.Branch)
	assert.Equal(t, "error", status.State)
	assert.True(t, status.Retryable)
	assert.NotEmpty(t, status.Error)
	current, err := service.Read(t.Context(), name)
	require.NoError(t, err)
	assert.Equal(t, "# Retained while commit is unavailable", current.Content)
	backend.fail = false
	_, err = service.Push(t.Context(), current.Revision)
	require.NoError(t, err)
	status, err = service.Status(t.Context())
	require.NoError(t, err)
	assert.False(t, status.Retryable)
	assert.Empty(t, status.Error)
	assert.False(t, status.Dirty)
}
