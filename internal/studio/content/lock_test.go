package content

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedLocationsCannotBeSharedByActiveServices(t *testing.T) {
	for _, shared := range []string{"root", "state", "output"} {
		t.Run(shared, func(t *testing.T) {
			service, original := managedFixture(t)
			base := t.TempDir()
			other := original
			other.Root = filepath.Join(base, "content")
			other.StateDir = filepath.Join(base, "state")
			other.OutputDir = filepath.Join(base, "site")
			switch shared {
			case "root":
				other.Root = original.Root
			case "state":
				other.StateDir = original.StateDir
			case "output":
				other.OutputDir = original.OutputDir
			}
			_, err := New(other)
			if shared == "state" {
				require.ErrorIs(t, err, ErrOwnership)
			} else {
				require.ErrorIs(t, err, ErrConflict)
			}
			if shared != "root" {
				_, err = os.Stat(filepath.Join(other.Root, "docs.json"))
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
			if shared != "state" {
				_, err = os.Stat(filepath.Join(other.StateDir, "owner.json"))
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
			require.NoError(t, service.Close())
			reopened, err := New(original)
			require.NoError(t, err)
			require.NoError(t, reopened.Close())
		})
	}
}

func TestFailedInitializationReleasesEveryLocationLock(t *testing.T) {
	base := t.TempDir()
	state := filepath.Join(base, "state")
	require.NoError(t, os.Mkdir(state, 0o700))
	foreign := filepath.Join(state, "unowned.txt")
	require.NoError(t, os.WriteFile(foreign, []byte("keep"), 0o600))
	config := Config{Root: filepath.Join(base, "content"), StateDir: state, OutputDir: filepath.Join(base, "site"), Initialize: true, Compile: func(context.Context, string, string) error { return nil }}
	_, err := New(config)
	require.ErrorIs(t, err, ErrOwnership)
	data, err := os.ReadFile(foreign)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))
	require.NoError(t, os.Remove(foreign))
	service, err := New(config)
	require.NoError(t, err)
	require.NoError(t, service.Close())
}
