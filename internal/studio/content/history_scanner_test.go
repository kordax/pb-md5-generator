package content

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const historyScannerLiveID = "11111111111111111111111111111111"
const historyScannerDeletedID = "22222222222222222222222222222222"
const historyScannerMarkerID = "33333333333333333333333333333333"
const historyScannerKnownHash = "4e404754257a600b2d953b1b0747abf4097cbd45b792cae00ffaf4cb39ad20ea"
const historyScannerLiveMetadata = `{"id":"11111111111111111111111111111111","path":"guide.md","revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","deleted":false,"size":6}`
const historyScannerDeletedMetadata = `{"id":"22222222222222222222222222222222","path":"gone.md","revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","deleted":true,"size":0}`

func prepareHistoryScannerFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	// Deliberately create the files out of lexical order. The expected digest
	// is a fixed public-format vector, not another scanner implementation.
	for _, file := range []struct{ name, data string }{
		{historyScannerDeletedID + ".json", historyScannerDeletedMetadata},
		{deleteTreeGeneration, historyScannerMarkerID},
		{historyScannerLiveID + ".bin", "alpha\n"},
		{historyScannerLiveID + ".json", historyScannerLiveMetadata},
	} {
		require.NoError(t, os.WriteFile(filepath.Join(directory, file.name), []byte(file.data), 0o600))
	}
	return directory
}

func TestHistoryScannerKnownHashAndByteCountPreserveGenerationFormat(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		marker string
		bytes  int64
	}{
		{name: "published-marker", marker: historyScannerMarkerID, bytes: 360},
		{name: "no-marker", bytes: 328},
		{name: "another-valid-marker", marker: "44444444444444444444444444444444", bytes: 360},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := prepareHistoryScannerFixture(t)
			marker := filepath.Join(directory, deleteTreeGeneration)
			if scenario.marker == "" {
				require.NoError(t, os.Remove(marker))
			} else {
				require.NoError(t, os.WriteFile(marker, []byte(scenario.marker), 0o600))
			}
			hash, size, err := historyGeneration(t.Context(), directory)
			require.NoError(t, err)
			assert.Equal(t, historyScannerKnownHash, hash)
			assert.Equal(t, scenario.bytes, size)
			files, revisions, size, err := publicationHistoryFiles(t.Context(), directory, true)
			require.NoError(t, err)
			assert.Equal(t, historyScannerKnownHash, publicationHistoryHash(files))
			assert.Equal(t, scenario.bytes, size)
			require.Len(t, revisions, 2)
			assert.Equal(t, historyScannerLiveID, revisions[0].ID)
			assert.Equal(t, historyScannerDeletedID, revisions[1].ID)
			assert.False(t, revisions[0].Deleted)
			assert.True(t, revisions[1].Deleted)
		})
	}
}

func TestHistoryScannerCompleteValidationAgreesForDamagedGenerations(t *testing.T) {
	for _, scenario := range []string{"missing-payload", "orphan-payload", "tombstone-with-payload", "wrong-payload-size", "invalid-marker"} {
		t.Run(scenario, func(t *testing.T) {
			directory := prepareHistoryScannerFixture(t)
			switch scenario {
			case "missing-payload":
				require.NoError(t, os.Remove(filepath.Join(directory, historyScannerLiveID+".bin")))
			case "orphan-payload":
				require.NoError(t, os.WriteFile(filepath.Join(directory, "44444444444444444444444444444444.bin"), []byte("orphan"), 0o600))
			case "tombstone-with-payload":
				require.NoError(t, os.WriteFile(filepath.Join(directory, historyScannerDeletedID+".bin"), nil, 0o600))
			case "wrong-payload-size":
				wrong := strings.Replace(historyScannerLiveMetadata, `"size":6`, `"size":7`, 1)
				require.NoError(t, os.WriteFile(filepath.Join(directory, historyScannerLiveID+".json"), []byte(wrong), 0o600))
			case "invalid-marker":
				require.NoError(t, os.WriteFile(filepath.Join(directory, deleteTreeGeneration), []byte("not a transaction ID"), 0o600))
			}
			hash, size, err := historyGeneration(t.Context(), directory)
			require.ErrorIs(t, err, ErrOwnership)
			assert.Empty(t, hash)
			assert.Zero(t, size)
			_, _, _, err = publicationHistoryFiles(t.Context(), directory, true)
			require.ErrorIs(t, err, ErrOwnership)
		})
	}
}

func TestHistoryScannerWrapperRootOpeningPoliciesRemainDistinct(t *testing.T) {
	t.Run("missing-root", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "missing")
		_, _, oldErr := historyGeneration(t.Context(), directory)
		require.ErrorIs(t, oldErr, os.ErrNotExist)
		assert.False(t, errors.Is(oldErr, ErrOwnership))
		_, _, _, newErr := publicationHistoryFiles(t.Context(), directory, true)
		require.ErrorIs(t, newErr, os.ErrNotExist)
		assert.ErrorIs(t, newErr, ErrOwnership)
	})
	t.Run("regular-file-root", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(directory, []byte("a file cannot be a history directory"), 0o600))
		_, _, oldErr := historyGeneration(t.Context(), directory)
		require.Error(t, oldErr)
		var pathErr *os.PathError
		assert.ErrorAs(t, oldErr, &pathErr)
		assert.False(t, errors.Is(oldErr, ErrOwnership))
		_, _, _, newErr := publicationHistoryFiles(t.Context(), directory, true)
		require.ErrorIs(t, newErr, ErrOwnership)
	})
	t.Run("symlink-root", func(t *testing.T) {
		directory := prepareHistoryScannerFixture(t)
		alias := filepath.Join(t.TempDir(), "alias")
		err := os.Symlink(directory, alias)
		if err != nil && runtime.GOOS == "windows" {
			t.Skipf("Windows did not permit creating the symlink fixture: %v", err)
		}
		require.NoError(t, err)
		hash, size, err := historyGeneration(t.Context(), alias)
		require.NoError(t, err)
		assert.Equal(t, historyScannerKnownHash, hash)
		assert.Equal(t, int64(360), size)
		_, _, _, err = publicationHistoryFiles(t.Context(), alias, true)
		require.ErrorIs(t, err, ErrOwnership)
	})
}

func TestHistoryScannerPreservesLegacyMalformedNameErrorWithoutRelaxingPublicationNames(t *testing.T) {
	directory := t.TempDir()
	file, err := os.Create(filepath.Join(directory, historyScannerLiveID+".json.bin"))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(int64(MaxBinaryBytes)+1))
	require.NoError(t, file.Close())
	// The shipped scanner applies its size bound before discovering the
	// orphan double-extension payload. The publication scanner rejects its
	// noncanonical name immediately, including for a partial placement.
	_, _, oldErr := historyGeneration(t.Context(), directory)
	require.ErrorIs(t, oldErr, ErrLimit)
	for _, complete := range []bool{true, false} {
		_, _, _, err := publicationHistoryFiles(t.Context(), directory, complete)
		require.ErrorIs(t, err, ErrOwnership)
	}
}
