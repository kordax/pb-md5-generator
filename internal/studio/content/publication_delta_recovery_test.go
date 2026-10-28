package content

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareDeltaRetentionRecoveryFixture(t *testing.T) (publicationRecoveryFixture, string) {
	t.Helper()
	fixture := preparePublicationRecoveryFixture(t)
	require.NoError(t, fixture.transaction.rollback(t.Context()))
	require.NoError(t, fixture.transaction.cleanup())
	history, err := fixture.service.History(t.Context(), "index.md")
	require.NoError(t, err)
	require.NotEmpty(t, history)
	retained := history[0].ID + ".bin"
	existing, err := fixture.service.revisions()
	require.NoError(t, err)
	for i := range MaxHistory - len(existing) {
		revision := history[0]
		revision.ID = fmt.Sprintf("%032x", i+4096)
		revision.Revision = fixture.before.Revision
		revision.CreatedAt = time.Unix(int64(i+1), 0).UTC()
		revision.Deleted = false
		revision.Size = int64(len(fixture.original))
		metadata, err := json.Marshal(revision)
		require.NoError(t, err)
		base := filepath.Join(fixture.config.StateDir, "history", revision.ID)
		require.NoFileExists(t, base+".json")
		require.NoError(t, os.WriteFile(base+".json", metadata, 0o600))
		require.NoError(t, os.WriteFile(base+".bin", fixture.original, 0o600))
	}
	stage, id, err := createStorageStage(fixture.config.StateDir, publicationTransactionPrefix)
	require.NoError(t, err)
	before, after, err := fixture.service.candidate(t.Context(), stage, draftChanges(fixture.draft), editor, true)
	require.NoError(t, err)
	paths := changedFiles(before, after)
	backup := filepath.Join(stage, "backup")
	require.NoError(t, os.Mkdir(backup, 0o700))
	require.NoError(t, applyFiles(t.Context(), backup, fixture.config.Root, paths, before))
	status, err := fixture.service.cfg.Storage.Status(t.Context())
	require.NoError(t, err)
	transaction, err := fixture.service.preparePublication(t.Context(), stage, id, publicationPlan{Before: before, After: after, Paths: paths, Draft: fixture.draft, Input: PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, Actor: editor, Status: status})
	require.NoError(t, err)
	require.NoError(t, transaction.installHistory(t.Context()))
	fixture.stage, fixture.id, fixture.transaction = stage, id, transaction
	fixture.before, fixture.after, fixture.paths = before, after, paths
	return fixture, retained
}

func publishDeltaRecoveryFixture(t *testing.T, fixture publicationRecoveryFixture) {
	t.Helper()
	fixture.applySources(t)
	published, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, published)
	require.NoError(t, fixture.transaction.markPublished())
}

func deltaRecoveryAddedNames(plan publicationHistoryDelta) []string {
	names := make([]string, 0, len(plan.Added))
	for name := range plan.Added {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func assertDeltaPrivateCopies(t *testing.T, fixture publicationRecoveryFixture, plan publicationHistoryDelta) {
	t.Helper()
	before, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.original, before)
	after, err := os.ReadFile(filepath.Join(fixture.stage, "source", "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.replacement, after)
	for _, name := range deltaRecoveryAddedNames(plan) {
		data, err := os.ReadFile(filepath.Join(fixture.stage, "history-next", name))
		require.NoError(t, err)
		hash := sha256.Sum256(data)
		assert.Equal(t, plan.Added[name].Hash, hex.EncodeToString(hash[:]))
	}
	require.FileExists(t, filepath.Join(fixture.config.StateDir, publicationJournalName))
	require.DirExists(t, filepath.Join(fixture.transaction.outputParent(), fixture.transaction.outputHolder(), "previous"))
}

func TestPublicationDeltaRestartCompletesPartialHistoryWithoutRewritingRetainedPayload(t *testing.T) {
	for _, step := range []string{"first-old-file-evicted", "first-new-file-installed"} {
		t.Run(step, func(t *testing.T) {
			fixture, retainedName := prepareDeltaRetentionRecoveryFixture(t)
			plan, err := fixture.transaction.historyPlan()
			require.NoError(t, err)
			require.NotEmpty(t, plan.Removed)
			require.NotEmpty(t, plan.Added)
			retainedPath := filepath.Join(fixture.config.StateDir, "history", retainedName)
			retainedInfo, err := os.Stat(retainedPath)
			require.NoError(t, err)
			retainedBytes, err := os.ReadFile(retainedPath)
			require.NoError(t, err)
			publishDeltaRecoveryFixture(t, fixture)
			if step == "first-old-file-evicted" {
				name := plan.Removed[0]
				require.NoError(t, os.Rename(filepath.Join(fixture.config.StateDir, "history", name), filepath.Join(fixture.stage, "history-previous", name)))
			} else {
				for _, name := range plan.Removed {
					require.NoError(t, os.Rename(filepath.Join(fixture.config.StateDir, "history", name), filepath.Join(fixture.stage, "history-previous", name)))
				}
				name := deltaRecoveryAddedNames(plan)[0]
				require.NoError(t, os.Rename(filepath.Join(fixture.stage, "history-next", name), filepath.Join(fixture.config.StateDir, "history", name)))
			}
			reopened, err := fixture.reopen(t)
			require.NoError(t, err)
			current, err := reopened.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.replacement), current.Content)
			history, err := reopened.History(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Greater(t, len(history), MaxHistory/2)
			allHistory, err := reopened.revisions()
			require.NoError(t, err)
			assert.Equal(t, MaxHistory, len(allHistory))
			assert.Equal(t, fixture.after.Revision, history[0].Revision)
			entries, err := os.ReadDir(filepath.Join(fixture.config.StateDir, "history"))
			require.NoError(t, err)
			assert.LessOrEqual(t, len(entries), MaxHistory*2)
			for _, name := range plan.Removed {
				assert.NoFileExists(t, filepath.Join(fixture.config.StateDir, "history", name))
			}
			for name := range plan.Added {
				assert.FileExists(t, filepath.Join(fixture.config.StateDir, "history", name))
			}
			currentInfo, err := os.Stat(retainedPath)
			require.NoError(t, err)
			assert.True(t, os.SameFile(retainedInfo, currentInfo), "a retained immutable payload must keep its inode")
			assert.Equal(t, retainedInfo.ModTime(), currentInfo.ModTime())
			currentBytes, err := os.ReadFile(retainedPath)
			require.NoError(t, err)
			assert.Equal(t, retainedBytes, currentBytes)
			assert.NoDirExists(t, fixture.stage)
			assert.NoFileExists(t, filepath.Join(fixture.config.StateDir, publicationJournalName))
			draft, err := reopened.ReadDraft(t.Context(), fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.Revision, draft.Revision)
		})
	}
}

func TestPublicationDeltaRestartRetainsUnknownHistoryPayloadUntilRepaired(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	plan, err := fixture.transaction.historyPlan()
	require.NoError(t, err)
	var name string
	for candidate := range plan.Before {
		if strings.HasSuffix(candidate, ".bin") {
			name = candidate
			break
		}
	}
	require.NotEmpty(t, name)
	publishDeltaRecoveryFixture(t, fixture)
	payload := filepath.Join(fixture.config.StateDir, "history", name)
	original, err := os.ReadFile(payload)
	require.NoError(t, err)
	require.NotEmpty(t, original)
	unknown := append([]byte(nil), original...)
	unknown[0] ^= 1
	require.NoError(t, os.WriteFile(payload, unknown, 0o600))
	_, err = fixture.reopen(t)
	require.ErrorIs(t, err, ErrOwnership)
	retained, err := os.ReadFile(payload)
	require.NoError(t, err)
	assert.Equal(t, unknown, retained, "recovery must not overwrite external history")
	assertDeltaPrivateCopies(t, fixture, plan)
	require.NoError(t, os.WriteFile(payload, original, 0o600))
	reopened, err := fixture.reopen(t)
	require.NoError(t, err)
	current, err := reopened.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), current.Content)
}

func TestPublicationDeltaRestartChecksPlanRawSHAAndRetainsEveryCopy(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	plan, err := fixture.transaction.historyPlan()
	require.NoError(t, err)
	publishDeltaRecoveryFixture(t, fixture)
	filename := filepath.Join(fixture.stage, publicationHistoryPlanName)
	original, err := os.ReadFile(filename)
	require.NoError(t, err)
	modified := append(append([]byte(nil), original...), '\n')
	var validJSON publicationHistoryDelta
	require.NoError(t, json.Unmarshal(modified, &validJSON))
	require.NoError(t, os.WriteFile(filename, modified, 0o600))
	_, err = fixture.reopen(t)
	require.ErrorIs(t, err, ErrOwnership)
	retained, err := os.ReadFile(filename)
	require.NoError(t, err)
	assert.Equal(t, modified, retained)
	assertDeltaPrivateCopies(t, fixture, plan)
	require.NoError(t, os.WriteFile(filename, original, 0o600))
	reopened, err := fixture.reopen(t)
	require.NoError(t, err)
	current, err := reopened.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), current.Content)
}

func TestPublicationDeltaCompletedRollbackMissingStagePreservesDraftAndOriginals(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	require.NoError(t, fixture.transaction.rollback(t.Context()))
	require.NoError(t, removeStorageTree(fixture.config.StateDir, fixture.transaction.name()))
	require.FileExists(t, filepath.Join(fixture.config.StateDir, publicationJournalName))
	reopened, err := fixture.reopen(t)
	require.NoError(t, err)
	current, err := reopened.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.original), current.Content)
	output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, output)
	draft, err := reopened.ReadDraft(t.Context(), fixture.draft.ID)
	require.NoError(t, err)
	assert.Equal(t, fixture.draft.Revision, draft.Revision)
	assert.Equal(t, fixture.draft.BaseRevision, draft.BaseRevision)
	assert.Equal(t, fixture.draft.Actor.ID, draft.Actor.ID)
	history, err := reopened.History(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Len(t, history, 2, "an aborted publication must not add history records")
	assert.NoFileExists(t, filepath.Join(fixture.config.StateDir, publicationJournalName))
}
