package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationDirectoryReadIsBoundedAndClosesHandle(t *testing.T) {
	for _, name := range []string{"empty", "bounded"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			if name == "bounded" {
				for _, filename := range []string{"first.md", "second.md", "third.md"} {
					require.NoError(t, os.WriteFile(filepath.Join(directory, filename), []byte(filename), 0o600))
				}
			}
			opened, err := os.Open(directory)
			require.NoError(t, err)
			entries, err := readPublicationDirectoryEntries(opened, 2)
			require.NoError(t, err)
			if name == "empty" {
				assert.Empty(t, entries, "empty directory EOF must be a successful bounded read")
			} else {
				assert.Len(t, entries, 2)
				remaining, err := os.ReadDir(directory)
				require.NoError(t, err)
				assert.Len(t, remaining, 3, "reading must not change the directory inventory")
			}
			_, err = opened.Stat()
			assert.ErrorIs(t, err, os.ErrClosed, "every successful read must release its directory handle")
		})
	}
}

func TestPublicationDirectoryReadFailureAlsoClosesHandle(t *testing.T) {
	name := filepath.Join(t.TempDir(), "regular.md")
	require.NoError(t, os.WriteFile(name, []byte("This is a regular file, not a directory."), 0o600))
	opened, err := os.Open(name)
	require.NoError(t, err)
	_, err = readPublicationDirectoryEntries(opened, 2)
	require.Error(t, err)
	_, err = opened.Stat()
	assert.ErrorIs(t, err, os.ErrClosed, "a failed directory read must not leak its opened handle")
	contents, err := os.ReadFile(name)
	require.NoError(t, err)
	assert.Equal(t, "This is a regular file, not a directory.", string(contents))
}
