package studio

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeRejectsMissingAndCancelledContextsWithoutCreatingState(t *testing.T) {
	config := DefaultConfig(filepath.Join(t.TempDir(), "untouched"))
	var missing context.Context
	_, err := Start(missing, config)
	assert.Error(t, err)
	assert.Error(t, Run(missing, config))
	assert.Error(t, Bootstrap(missing, config, "owner", "Owner", []byte(ownerTestPassword)))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = Start(ctx, config)
	assert.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, Bootstrap(ctx, config, "owner", "Owner", []byte(ownerTestPassword)), context.Canceled)
	_, err = os.Stat(config.DataDir)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestRuntimeConstructionFailureClosesTransferredListener(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	address := config.Listener.Addr().String()
	config.Backend = "git" // No Git repository has been provisioned.
	_, err := Start(t.Context(), config)
	require.Error(t, err)
	probe, err := net.Listen("tcp", address)
	require.NoError(t, err)
	require.NoError(t, probe.Close())
	// The auth constructor succeeded before the content constructor failed.
	// It must release its state lock as well as the network listener.
	assert.ErrorIs(t, Bootstrap(t.Context(), config, "another", "Another", []byte(ownerTestPassword)), auth.ErrConflict)
	config.Backend, config.Listener, config.Address = "local", nil, address
	server, err := Start(t.Context(), config)
	require.NoError(t, err)
	require.NoError(t, server.Close(t.Context()))
}

func TestRuntimeImmediateAndCancelledShutdownReleaseListener(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate", true: "cancelled"}[cancelled], func(t *testing.T) {
			config := runtimeConfigForTest(t)
			address := config.Listener.Addr().String()
			server, err := Start(t.Context(), config)
			require.NoError(t, err)
			stop := t.Context()
			if cancelled {
				var cancel context.CancelFunc
				stop, cancel = context.WithCancel(stop)
				cancel()
			}
			err = server.Close(stop)
			if cancelled && err != nil {
				assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
			} else {
				assert.NoError(t, err)
			}
			select {
			case <-server.http.stopped:
			case <-time.After(time.Second):
				t.Fatal("HTTP serve loop remained alive after shutdown")
			}
			probe, err := net.Listen("tcp", address)
			require.NoError(t, err)
			require.NoError(t, probe.Close())
		})
	}
}

func TestPortableManagementPathsAndDependencyAliases(t *testing.T) {
	editor := &auth.User{ID: "editor", Role: auth.RoleEditor}
	for _, name := range []string{"docs.json", "DOCS.JSON", "DOCſ.JSON", "themes", "Themes/x.json", "Themeſ/x.json", ".git/config"} {
		assert.False(t, canAccessSource(editor, auth.ActionRead, name), name)
		assert.False(t, canAccessSource(editor, auth.ActionWrite, name), name)
	}
	assert.True(t, canAccessSource(editor, auth.ActionWrite, "public/guide.md"))
	for _, test := range []struct{ name, parent string }{
		{"public/schema.yaml", "PUBLIC/SCHEMA.YAML"},
		{"public/comps.yaml", "PUBLIC/COMPſ.YAML"},
		{"public/components/value.yaml", "PUBLIC/COMPONENTS"},
	} {
		assert.True(t, sourceUnder(test.name, test.parent))
	}
	assert.False(t, sourceUnder("publicity/x.md", "public"))
	assert.False(t, sourceUnder("public", "public/child"))
}
