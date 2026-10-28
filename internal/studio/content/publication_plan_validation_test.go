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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func planValidationImageName(index int, extension string) string {
	return fmt.Sprintf("%032x.%s", index+1048576, extension)
}

// These are externally rewritten descriptors, not synthetic live history.
// Binding the journal to their exact bytes exercises semantic validation
// before recovery may trust any of their claimed files, sizes or hashes.
func malformedPersistedPublicationPlan(t *testing.T, scenario string, plan *publicationHistoryDelta) []byte {
	t.Helper()
	image := publicationHistoryFile{Hash: strings.Repeat("a", 64), Size: 1}
	beforeNames := make([]string, 0, len(plan.Before))
	for name := range plan.Before {
		beforeNames = append(beforeNames, name)
	}
	sort.Strings(beforeNames)
	require.NotEmpty(t, beforeNames)
	name := planValidationImageName(0, "bin")
	require.NotContains(t, plan.Before, name)
	require.NotContains(t, plan.Added, name)
	switch scenario {
	case "missing-before", "missing-added", "nonobject-before":
		// Rewrite the JSON envelope below without changing other fields.
	case "unsafe-image-name":
		plan.Added["../escape.bin"] = image
	case "invalid-image-hash":
		image.Hash = "not-a-sha256"
		plan.Added[name] = image
	case "negative-image-size":
		image.Size = -1
		plan.Added[name] = image
	case "oversized-json-image":
		image.Size = (64 << 10) + 1
		plan.Added[planValidationImageName(0, "json")] = image
	case "oversized-payload-image":
		image.Size = MaxBinaryBytes + 1
		plan.Added[name] = image
	case "duplicate-eviction":
		plan.Removed = []string{beforeNames[0], beforeNames[0]}
	case "unknown-eviction":
		plan.Removed = []string{name}
	case "overlapping-before-and-added":
		plan.Added[beforeNames[0]] = plan.Before[beforeNames[0]]
	case "added-generation-marker":
		image.Size = 32
		plan.Added[deleteTreeGeneration] = image
	case "retained-generation-marker":
		image.Size = 32
		plan.Before[deleteTreeGeneration] = image
	case "too-many-before-images":
		plan.Before = make(map[string]publicationHistoryFile)
		for i := range 2*MaxHistory + 2 {
			plan.Before[planValidationImageName(i, "bin")] = image
		}
	case "too-many-result-files":
		plan.Added = make(map[string]publicationHistoryFile)
		count := 2*MaxHistory + 1 - len(plan.Before)
		require.Positive(t, count)
		require.LessOrEqual(t, count, 2*MaxHistory)
		for i := range count {
			plan.Added[planValidationImageName(i, "bin")] = image
		}
	case "too-many-records":
		plan.Added = make(map[string]publicationHistoryFile)
		image.Size = 256
		for i := range MaxHistory + 1 {
			plan.Added[planValidationImageName(i, "json")] = image
		}
	case "history-byte-budget":
		plan.Added = make(map[string]publicationHistoryFile)
		image.Size = MaxBinaryBytes
		for i := range int(MaxStateBytes/MaxBinaryBytes) + 1 {
			plan.Added[planValidationImageName(i, "bin")] = image
		}
	default:
		t.Fatalf("unrecognized malformed-plan scenario: %s", scenario)
	}
	data, err := json.Marshal(plan)
	require.NoError(t, err)
	if scenario == "missing-before" || scenario == "missing-added" || scenario == "nonobject-before" {
		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &envelope))
		switch scenario {
		case "missing-before":
			delete(envelope, "before")
		case "missing-added":
			delete(envelope, "added")
		case "nonobject-before":
			envelope["before"] = json.RawMessage(`[]`)
		}
		data, err = json.Marshal(envelope)
		require.NoError(t, err)
	}
	return data
}

func TestPublicationPersistedPlanValidationRefusesTamperedContractsWithoutLosingEvidence(t *testing.T) {
	for _, scenario := range []struct {
		name string
		want error
	}{
		{name: "missing-before", want: ErrOwnership},
		{name: "missing-added", want: ErrOwnership},
		{name: "nonobject-before", want: ErrOwnership},
		{name: "unsafe-image-name", want: ErrOwnership},
		{name: "invalid-image-hash", want: ErrOwnership},
		{name: "negative-image-size", want: ErrOwnership},
		{name: "oversized-json-image", want: ErrOwnership},
		{name: "oversized-payload-image", want: ErrOwnership},
		{name: "duplicate-eviction", want: ErrOwnership},
		{name: "unknown-eviction", want: ErrOwnership},
		{name: "overlapping-before-and-added", want: ErrOwnership},
		{name: "added-generation-marker", want: ErrOwnership},
		{name: "retained-generation-marker", want: ErrOwnership},
		{name: "too-many-before-images", want: ErrOwnership},
		{name: "too-many-result-files", want: ErrOwnership},
		{name: "too-many-records", want: ErrLimit},
		{name: "history-byte-budget", want: ErrLimit},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			planPath := filepath.Join(fixture.stage, publicationHistoryPlanName)
			journalPath := filepath.Join(fixture.config.StateDir, publicationJournalName)
			cleanPlan, err := os.ReadFile(planPath)
			require.NoError(t, err)
			cleanJournal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			var plan publicationHistoryDelta
			require.NoError(t, json.Unmarshal(cleanPlan, &plan))
			var journal publicationJournal
			require.NoError(t, json.Unmarshal(cleanJournal, &journal))
			// This is a real interruption after source application and before
			// output publication. Invalid recovery input must not roll it back
			// or erase either the old or new private source image.
			fixture.applySources(t)
			malformed := malformedPersistedPublicationPlan(t, scenario.name, &plan)
			digest := sha256.Sum256(malformed)
			journal.HistoryPlan = hex.EncodeToString(digest[:])
			journal.PreviousHistory = publicationHistoryHash(plan.Before)
			journal.NextHistory = publicationHistoryHash(plan.after())
			badJournal, err := json.Marshal(journal)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(planPath, malformed, 0o600))
			require.NoError(t, os.WriteFile(journalPath, badJournal, 0o600))
			contract := &publicationTransaction{service: fixture.service, journal: journal}
			_, err = contract.historyPlan()
			require.ErrorIs(t, err, scenario.want, "raw SHA and aggregate digests are already coherent")
			evidence := capturePublicationOutputProof(t, fixture)
			historyEvidence := snapshotPublicationOutputProof(t, filepath.Join(fixture.config.StateDir, "history"))
			_, err = fixture.reopen(t)
			require.ErrorIs(t, err, scenario.want)
			assertPublicationOutputProofUnchanged(t, fixture, evidence)
			assert.Equal(t, historyEvidence, snapshotPublicationOutputProof(t, filepath.Join(fixture.config.StateDir, "history")))
			retainedPlan, err := os.ReadFile(planPath)
			require.NoError(t, err)
			assert.Equal(t, malformed, retainedPlan)
			retainedJournal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			assert.Equal(t, badJournal, retainedJournal)
			require.NoError(t, os.WriteFile(planPath, cleanPlan, 0o600))
			require.NoError(t, os.WriteFile(journalPath, cleanJournal, 0o600))
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
			assert.Equal(t, draftChanges(fixture.draft), draftChanges(draft))
			assert.NoDirExists(t, fixture.stage)
			assert.NoFileExists(t, journalPath)
		})
	}
}
