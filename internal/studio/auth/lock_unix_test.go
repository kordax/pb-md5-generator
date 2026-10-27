//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package auth

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ownerTestInfo struct {
	os.FileInfo
	metadata any
}

func (info ownerTestInfo) Sys() any { return info.metadata }

func TestPrivateStateOwnershipMatchesEffectiveUser(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	require.NoError(t, err)
	assert.True(t, ownedByCurrentUser(info))
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	other := *stat
	other.Uid ^= 1
	assert.False(t, ownedByCurrentUser(ownerTestInfo{FileInfo: info, metadata: &other}))
	assert.False(t, ownedByCurrentUser(ownerTestInfo{FileInfo: info, metadata: nil}))
	assert.False(t, ownedByCurrentUser(ownerTestInfo{FileInfo: info, metadata: (*syscall.Stat_t)(nil)}))
}
