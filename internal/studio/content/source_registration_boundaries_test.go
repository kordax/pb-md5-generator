package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterSourceRequiresReviewedProjectAndExactSourceBytes(t *testing.T) {
	for _, conflict := range []string{"old project", "wrong source", "changed source"} {
		t.Run(conflict, func(t *testing.T) {
			service, config, input := registrationFixture(t, "source.md", "# Reviewed\n")
			retained := saveText(t, service, "unrelated.md", "# Owner draft\n")
			want := "# Reviewed\n"
			switch conflict {
			case "old project":
				input.Revision = strings.Repeat("0", 64)
			case "wrong source":
				input.SourceRevision = strings.Repeat("0", 64)
			case "changed source":
				want = "# Changed\n"
				require.NoError(t, os.WriteFile(filepath.Join(config.Root, input.Path), []byte(want), 0o600))
			}
			_, err := service.RegisterSource(context.Background(), input, editor)
			assert.ErrorIs(t, err, ErrConflict)
			after, err := service.ReadDraft(context.Background(), retained.ID)
			require.NoError(t, err)
			assert.Equal(t, retained, after)
			drafts, err := service.Drafts(context.Background())
			require.NoError(t, err)
			assert.Len(t, drafts, 1)
			data, err := os.ReadFile(filepath.Join(config.Root, input.Path))
			require.NoError(t, err)
			assert.Equal(t, want, string(data))
		})
	}
}

func TestRegisterSourceReauthorizesBeforeSavingAndRechecksReviewedBytes(t *testing.T) {
	for _, change := range []string{"revoked", "changed", "removed"} {
		t.Run(change, func(t *testing.T) {
			service, config, input := registrationFixture(t, "source.md", "# Reviewed\n")
			retained := saveText(t, service, "unrelated.md", "# Recoverable draft\n")
			calls := 0
			service.cfg.AuthorizeSource = func(_ context.Context, actor Actor, name string, access SourceAccess) error {
				assert.Equal(t, editor.ID, actor.ID)
				assert.Equal(t, input.Path, name)
				assert.Equal(t, SourceReadWrite, access)
				calls++
				if calls != 2 {
					return nil
				}
				switch change {
				case "revoked":
					return ErrOwnership
				case "changed":
					return os.WriteFile(filepath.Join(config.Root, name), []byte("# External\n"), 0o600)
				default:
					return os.Remove(filepath.Join(config.Root, name))
				}
			}
			_, err := service.RegisterSource(context.Background(), input, editor)
			switch change {
			case "revoked":
				assert.ErrorIs(t, err, ErrOwnership)
			case "changed":
				assert.ErrorIs(t, err, ErrConflict)
			default:
				assert.ErrorIs(t, err, ErrNotFound)
			}
			assert.Equal(t, 2, calls)
			after, err := service.ReadDraft(context.Background(), retained.ID)
			require.NoError(t, err)
			assert.Equal(t, retained, after)
			drafts, err := service.Drafts(context.Background())
			require.NoError(t, err)
			assert.Len(t, drafts, 1)
			project, err := service.Project(context.Background())
			require.NoError(t, err)
			assert.Len(t, project.Documents, 1)
			data, err := os.ReadFile(filepath.Join(config.Root, input.Path))
			switch change {
			case "revoked":
				require.NoError(t, err)
				assert.Equal(t, "# Reviewed\n", string(data))
			case "changed":
				require.NoError(t, err)
				assert.Equal(t, "# External\n", string(data))
			default:
				assert.True(t, os.IsNotExist(err), "registration must not recreate an externally removed source")
			}
		})
	}
}

func TestRegisterSourceChecksFreshAuthorizationAfterWaitingForGate(t *testing.T) {
	service, config, input := registrationFixture(t, "source.md", "# Reviewed\n")
	var denied atomic.Bool
	service.cfg.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error {
		if denied.Load() {
			return ErrOwnership
		}
		return nil
	}
	_, release, err := service.enter(context.Background())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		_, registerErr := service.RegisterSource(ctx, input, editor)
		result <- registerErr
	}()
	<-started
	denied.Store(true)
	release()
	assert.ErrorIs(t, <-result, ErrOwnership)
	data, err := os.ReadFile(filepath.Join(config.Root, input.Path))
	require.NoError(t, err)
	assert.Equal(t, "# Reviewed\n", string(data))
	drafts, err := service.Drafts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, drafts)
}

func TestRegisterSourceFailsClosedWithoutHookAndAfterCancellation(t *testing.T) {
	service, _, input := registrationFixture(t, "source.md", "# Reviewed\n")
	service.cfg.AuthorizeSource = nil
	_, err := service.RegisterSource(context.Background(), input, editor)
	assert.ErrorIs(t, err, ErrUnsupported)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.RegisterSource(ctx, input, editor)
	assert.ErrorIs(t, err, context.Canceled)
	drafts, err := service.Drafts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, drafts)
}

func TestRegisterSourceRejectsInvalidMetadataBeforeCreatingDraft(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RegisterSourceInput)
	}{
		{name: "missing kind", mutate: func(input *RegisterSourceInput) { input.Kind = "" }},
		{name: "unknown kind", mutate: func(input *RegisterSourceInput) { input.Kind = "script" }},
		{name: "blank title", mutate: func(input *RegisterSourceInput) { input.Title = " \t " }},
		{name: "control title", mutate: func(input *RegisterSourceInput) { input.Title = "A\nB" }},
		{name: "oversized title", mutate: func(input *RegisterSourceInput) { input.Title = strings.Repeat("x", 257) }},
		{name: "manifest", mutate: func(input *RegisterSourceInput) { input.Path = "docs.json" }},
		{name: "binary", mutate: func(input *RegisterSourceInput) { input.Path = "source.pdf" }},
		{name: "invalid id", mutate: func(input *RegisterSourceInput) { input.DocumentID = "Uppercase" }},
		{name: "invalid output", mutate: func(input *RegisterSourceInput) { input.OutputPath = "../outside.html" }},
		{name: "active asset output", mutate: func(input *RegisterSourceInput) { input.OutputPath = "assets/active.html" }},
		{name: "missing project revision", mutate: func(input *RegisterSourceInput) { input.Revision = "" }},
		{name: "missing source revision", mutate: func(input *RegisterSourceInput) { input.SourceRevision = "" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, config, input := registrationFixture(t, "source.md", "# Reviewed\n")
			test.mutate(&input)
			_, err := service.RegisterSource(context.Background(), input, editor)
			assert.ErrorIs(t, err, ErrInvalid)
			drafts, err := service.Drafts(context.Background())
			require.NoError(t, err)
			assert.Empty(t, drafts)
			data, err := os.ReadFile(filepath.Join(config.Root, "source.md"))
			require.NoError(t, err)
			assert.Equal(t, "# Reviewed\n", string(data))
		})
	}
}

func TestRegisterSourceRejectsUnsafeOrOversizedTextAndMissingFiles(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{name: "invalid UTF8", data: []byte{0xff, 0xfe}},
		{name: "NUL", data: []byte("# NUL\x00text\n")},
		{name: "oversized", data: bytes.Repeat([]byte("x"), MaxTextBytes+1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, config, input := registrationFixture(t, "source.md", "# Reviewed\n")
			retained := saveText(t, service, "unrelated.md", "# Retained\n")
			require.NoError(t, os.WriteFile(filepath.Join(config.Root, input.Path), test.data, 0o600))
			project, projectErr := service.Project(context.Background())
			if projectErr == nil {
				input.Revision = project.Revision
			}
			digest := sha256.Sum256(test.data)
			input.SourceRevision = hex.EncodeToString(digest[:])
			_, err := service.RegisterSource(context.Background(), input, editor)
			require.Error(t, err)
			after, err := service.ReadDraft(context.Background(), retained.ID)
			require.NoError(t, err)
			assert.Equal(t, retained, after)
			data, err := os.ReadFile(filepath.Join(config.Root, input.Path))
			require.NoError(t, err)
			assert.Equal(t, test.data, data)
		})
	}
	t.Run("missing", func(t *testing.T) {
		service, config, input := registrationFixture(t, "source.md", "# Reviewed\n")
		require.NoError(t, os.Remove(filepath.Join(config.Root, input.Path)))
		project, err := service.Project(context.Background())
		require.NoError(t, err)
		input.Revision = project.Revision
		_, err = service.RegisterSource(context.Background(), input, editor)
		assert.ErrorIs(t, err, ErrNotFound)
		drafts, err := service.Drafts(context.Background())
		require.NoError(t, err)
		assert.Empty(t, drafts)
	})
}

func TestRegisterSourceAllowsExplicitOutputForUnicodeSourceAndRetainsUnrelatedDraft(t *testing.T) {
	service, _, input := registrationFixture(t, "examples/Σ.txt", "Reviewed literal source\n")
	retained := saveText(t, service, "unrelated.md", "# Owner draft\n")
	_, err := service.RegisterSource(context.Background(), input, editor)
	assert.ErrorIs(t, err, ErrInvalid, "unsafe default output must not be silently slugified")
	input.OutputPath, input.DocumentID = "reference/sigma.html", "sigma"
	draft, err := service.RegisterSource(context.Background(), input, editor)
	require.NoError(t, err)
	assert.Equal(t, "reference/sigma.html", draft.Change.OutputPath)
	after, err := service.ReadDraft(context.Background(), retained.ID)
	require.NoError(t, err)
	assert.Equal(t, retained, after)
	drafts, err := service.Drafts(context.Background())
	require.NoError(t, err)
	assert.Len(t, drafts, 2)
}
