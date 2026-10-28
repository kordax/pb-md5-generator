package content

import (
	"bytes"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type publicationOutputLimitFile struct {
	info   os.FileInfo
	digest [32]byte
}

func publicationOutputLimitEvidence(t *testing.T, fixture publicationRecoveryFixture) map[string]publicationOutputLimitFile {
	t.Helper()
	files := make(map[string]publicationOutputLimitFile)
	record := func(name string) error {
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrOwnership
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		files[name] = publicationOutputLimitFile{info: info, digest: sha256.Sum256(data)}
		return nil
	}
	for name := range fixture.after.Files {
		require.NoError(t, record(filepath.Join(fixture.config.Root, filepath.FromSlash(name))))
	}
	for _, directory := range []string{fixture.stage, fixture.config.OutputDir} {
		require.NoError(t, filepath.WalkDir(directory, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			return record(name)
		}))
	}
	entries, err := os.ReadDir(fixture.config.StateDir)
	require.NoError(t, err)
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			require.NoError(t, record(filepath.Join(fixture.config.StateDir, entry.Name())))
		}
	}
	return files
}

func assertPublicationOutputLimitEvidence(t *testing.T, files map[string]publicationOutputLimitFile) {
	t.Helper()
	for name, before := range files {
		info, err := os.Lstat(name)
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular(), "known recovery evidence must remain a regular file")
		require.True(t, os.SameFile(before.info, info), "failed recovery must not replace known evidence")
		assert.Equal(t, before.info.ModTime(), info.ModTime(), "failed recovery must not silently rebuild or rewrite evidence")
		data, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.True(t, before.digest == sha256.Sum256(data), "failed recovery must preserve exact known evidence bytes")
	}
}

type publicationOutputLimitObstruction struct {
	verify func(*testing.T)
	remove func(*testing.T)
}

func oversizedPublicationOutput(t *testing.T, directory string) publicationOutputLimitObstruction {
	t.Helper()
	name := filepath.Join(directory, "output-limit-sparse.html")
	const size int64 = (512 << 20) + 1 // One byte beyond the complete output budget.
	marker := []byte("sparse-tail-evidence")
	file, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	require.NoError(t, err)
	truncateErr := file.Truncate(size)
	_, writeErr := file.WriteAt(marker, size-int64(len(marker)))
	closeErr := file.Close()
	require.NoError(t, truncateErr)
	require.NoError(t, writeErr)
	require.NoError(t, closeErr)
	info, err := os.Lstat(name)
	require.NoError(t, err)
	return publicationOutputLimitObstruction{
		verify: func(t *testing.T) {
			current, err := os.Lstat(name)
			require.NoError(t, err)
			require.True(t, os.SameFile(info, current))
			assert.Equal(t, size, current.Size())
			assert.Equal(t, info.ModTime(), current.ModTime())
			file, err := os.Open(name)
			require.NoError(t, err)
			data := make([]byte, len(marker))
			_, readErr := file.ReadAt(data, size-int64(len(marker)))
			closeErr := file.Close()
			require.NoError(t, readErr)
			require.NoError(t, closeErr)
			assert.True(t, bytes.Equal(marker, data))
		},
		remove: func(t *testing.T) { require.NoError(t, os.Remove(name)) },
	}
}

func symlinkPublicationOutput(t *testing.T, directory string) publicationOutputLimitObstruction {
	t.Helper()
	target := filepath.Join(t.TempDir(), "outside-output-target")
	marker := []byte("independent outside output evidence\n")
	require.NoError(t, os.WriteFile(target, marker, 0o600))
	info, err := os.Lstat(target)
	require.NoError(t, err)
	name := filepath.Join(directory, "output-limit-link.html")
	if err := os.Symlink(target, name); err != nil {
		t.Skip("requires local symlink support")
	}
	verifyTarget := func(t *testing.T) {
		current, err := os.Lstat(target)
		require.NoError(t, err)
		require.True(t, current.Mode().IsRegular())
		require.True(t, os.SameFile(info, current))
		assert.Equal(t, info.ModTime(), current.ModTime())
		data, err := os.ReadFile(target)
		require.NoError(t, err)
		assert.True(t, bytes.Equal(marker, data), "recovery must never follow the output link")
	}
	return publicationOutputLimitObstruction{
		verify: func(t *testing.T) {
			link, err := os.Readlink(name)
			require.NoError(t, err)
			assert.Equal(t, target, link)
			verifyTarget(t)
		},
		remove: func(t *testing.T) {
			require.NoError(t, os.Remove(name))
			verifyTarget(t)
		},
	}
}

func deepPublicationOutput(t *testing.T, directory string) publicationOutputLimitObstruction {
	t.Helper()
	name := filepath.Join(directory, "output-limit-depth")
	directories := []string{name}
	require.NoError(t, os.Mkdir(name, 0o700))
	for depth := 1; depth < 65; depth++ {
		name = filepath.Join(name, "d")
		require.NoError(t, os.Mkdir(name, 0o700))
		directories = append(directories, name)
	}
	return publicationOutputLimitObstruction{
		verify: func(t *testing.T) {
			for _, name := range directories {
				info, err := os.Lstat(name)
				require.NoError(t, err)
				assert.True(t, info.IsDir())
			}
		},
		remove: func(t *testing.T) {
			for index := len(directories) - 1; index >= 0; index-- {
				require.NoError(t, os.Remove(directories[index]))
			}
		},
	}
}

func caseFoldedPublicationOutput(t *testing.T, directory string) publicationOutputLimitObstruction {
	t.Helper()
	first, second := filepath.Join(directory, "output-limit-case.html"), filepath.Join(directory, "OUTPUT-LIMIT-CASE.html")
	marker := []byte("distinct external case collision evidence\n")
	require.NoError(t, os.WriteFile(first, marker, 0o600))
	file, err := os.OpenFile(second, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		require.NoError(t, os.Remove(first))
		t.Skip("requires distinct case-sensitive directory entries")
	}
	require.NoError(t, err)
	_, writeErr := file.Write(marker)
	closeErr := file.Close()
	require.NoError(t, writeErr)
	require.NoError(t, closeErr)
	return publicationOutputLimitObstruction{
		verify: func(t *testing.T) {
			for _, name := range []string{first, second} {
				data, err := os.ReadFile(name)
				require.NoError(t, err)
				assert.True(t, bytes.Equal(marker, data))
			}
		},
		remove: func(t *testing.T) {
			require.NoError(t, os.Remove(first))
			require.NoError(t, os.Remove(second))
		},
	}
}

func TestPublicationOutputLimitsRecovery(t *testing.T) {
	for _, test := range []struct {
		name   string
		want   error
		inject func(*testing.T, string) publicationOutputLimitObstruction
	}{
		{"oversized sparse HTML", ErrLimit, oversizedPublicationOutput},
		{"external symlink", ErrOwnership, symlinkPublicationOutput},
		{"depth beyond 64", ErrLimit, deepPublicationOutput},
		{"case collision", ErrOwnership, caseFoldedPublicationOutput},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			fixture.applySources(t)
			evidence := publicationOutputLimitEvidence(t, fixture)
			obstruction := test.inject(t, fixture.config.OutputDir)
			_, err := publicationOutputDigest(t.Context(), fixture.config.OutputDir, true)
			require.ErrorIs(t, err, test.want, "the actual output guard must reject the hostile tree")
			reopened, err := fixture.reopen(t)
			require.Error(t, err)
			require.Nil(t, reopened)
			assertPublicationOutputLimitEvidence(t, evidence)
			obstruction.verify(t)
			// The operator removes exactly the injected obstruction. No source,
			// backup, previous output, journal or history is edited to force recovery.
			obstruction.remove(t)
			recovered, err := fixture.reopen(t)
			require.NoError(t, err)
			file, err := recovered.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.original), file.Content)
			output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.True(t, bytes.Equal(fixture.previousSite, output))
			draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.Revision, draft.Revision)
			_, err = os.Lstat(fixture.stage)
			assert.True(t, os.IsNotExist(err), "only the verified completed rollback may remove its stage")
		})
	}
}
