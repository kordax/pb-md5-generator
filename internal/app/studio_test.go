package app

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCMSCommandsDispatchHelpAndRejectInvalidFlags(t *testing.T) {
	assert.Zero(t, Run([]string{"serve", "--help"}))
	assert.Zero(t, Run([]string{"admin-user", "--help"}))
	data := filepath.Join(t.TempDir(), "not-created")
	assert.Equal(t, 1, Run([]string{"serve", "--data-dir", data, "--storage", "unsupported"}))
	assert.NoDirExists(t, data)
	assert.Equal(t, 1, Run([]string{"admin-user", "--data-dir", data, "--username", "owner"}))
	assert.NoDirExists(t, data)
	assert.Contains(t, rootCommandUsage, "serve [flags]")
	assert.Contains(t, rootCommandUsage, "admin-user [flags]")
}
