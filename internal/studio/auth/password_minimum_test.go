package auth

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootstrapAcceptsTenCharacterPasswords(t *testing.T) {
	for _, alphabet := range []struct {
		name  string
		first rune
	}{
		{name: "ASCII", first: 'A'},
		{name: "Unicode", first: '\u4e00'},
	} {
		t.Run(alphabet.name, func(t *testing.T) {
			directory := t.TempDir()
			require.NoError(t, os.Chmod(directory, 0o700))
			service, err := Open(Config{StateDir: directory, PublicURL: "http://localhost:8080"})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })

			characters := make([]rune, 10)
			for i := range characters {
				characters[i] = alphabet.first + rune(i)
			}
			short := []byte(string(characters[:9]))
			password := []byte(string(characters))
			defer clear(short)
			defer clear(password)
			_, err = service.Bootstrap(t.Context(), "owner", "Owner", short)
			require.ErrorIs(t, err, ErrInvalid)
			assert.ErrorContains(t, err, "at least 10 Unicode characters")

			owner, err := service.Bootstrap(t.Context(), "owner", "Owner", password)
			require.NoError(t, err)
			assert.Equal(t, "owner", owner.Username)
		})
	}
}
