package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCLIBootstrapPersistsOneAdministratorAndAcceptsExactPassword(t *testing.T) {
	data := filepath.Join(t.TempDir(), "cms")
	password := "  a unique local administrator passphrase  "
	args := []string{"admin-user", "--data-dir", data, "--username", "owner", "--display-name", "Project owner", "--password-stdin"}
	var output bytes.Buffer
	require.NoError(t, RunCLI(context.Background(), args, strings.NewReader(password), &output, &output))
	assert.NotContains(t, output.String(), password)
	accounts, err := auth.Open(auth.Config{StateDir: filepath.Join(data, "state", "auth"), PublicURL: "http://localhost:8090"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, accounts.Close()) })
	assert.False(t, accounts.NeedsSetup())
	payload, err := json.Marshal(map[string]string{"username": "owner", "password": password})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8090/session", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:8090")
	response := httptest.NewRecorder()
	accounts.Handler().ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, accounts.Close())
	output.Reset()
	err = RunCLI(context.Background(), args, strings.NewReader(password), &output, &output)
	assert.ErrorIs(t, err, auth.ErrConflict)
	assert.NotContains(t, output.String(), password)
}

func TestServeCLIUsesExplicitPortableConfiguration(t *testing.T) {
	data := filepath.Join(t.TempDir(), "cms")
	var output bytes.Buffer
	called := false
	expected := errors.New("server stopped")
	err := runCLI(context.Background(), []string{"serve", "--data-dir", data, "--listen", "127.0.0.1:9123", "--public-url", "https://docs.example.test/team", "--storage", "git", "--git-remote", "upstream", "--init=false"}, nil, &output, io.Discard, cliActions{serve: func(ctx context.Context, config Config) error {
		called = true
		assert.NoError(t, ctx.Err())
		assert.Equal(t, data, config.DataDir)
		assert.Equal(t, "127.0.0.1:9123", config.Address)
		assert.Equal(t, "https://docs.example.test/team", config.PublicURL)
		assert.Equal(t, "git", config.Backend)
		assert.Equal(t, "upstream", config.GitRemote)
		assert.False(t, config.Initialize)
		return expected
	}})
	require.ErrorIs(t, err, expected)
	assert.True(t, called)
	assert.Contains(t, output.String(), "https://docs.example.test/team/_docs/admin/")
	assert.NoDirExists(t, data)
}

func TestCLIPasswordInputIsExactAndCleared(t *testing.T) {
	for _, password := range []string{"  a long unique passphrase  ", "a long unique passphrase\n", "Пароль из нескольких разных слов"} {
		t.Run("UTF-8 bytes", func(t *testing.T) {
			var output bytes.Buffer
			var retained []byte
			called := false
			err := runCLI(context.Background(), []string{"admin-user", "--data-dir", filepath.Join(t.TempDir(), "cms"), "--username", "owner", "--password-stdin"}, strings.NewReader(password), &output, &output, cliActions{bootstrap: func(_ context.Context, config Config, username, displayName string, value []byte) error {
				called = true
				assert.Equal(t, "owner", username)
				assert.Equal(t, "owner", displayName)
				assert.Equal(t, "http://localhost:8090", config.PublicURL)
				assert.Equal(t, password, string(value))
				retained = value
				return nil
			}})
			require.NoError(t, err)
			assert.True(t, called)
			assert.Equal(t, make([]byte, len(password)), retained)
			assert.NotContains(t, output.String(), password)
			assert.Contains(t, output.String(), "Administrator created")
		})
	}
}

type cliCountedReader struct {
	reader io.Reader
	read   int
}

func (r *cliCountedReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	r.read += n
	return n, err
}

func TestCLIRejectsUnboundedOrMalformedPasswordBeforeBootstrap(t *testing.T) {
	for _, password := range []string{"", string([]byte{0xff}), strings.Repeat("x", 1<<20)} {
		reader := &cliCountedReader{reader: strings.NewReader(password)}
		called := false
		var output bytes.Buffer
		err := runCLI(context.Background(), []string{"admin-user", "--username", "owner", "--password-stdin"}, reader, &output, &output, cliActions{bootstrap: func(context.Context, Config, string, string, []byte) error {
			called = true
			return nil
		}})
		require.Error(t, err)
		assert.False(t, called)
		assert.LessOrEqual(t, reader.read, maxCLIPasswordBytes+1)
		assert.Empty(t, output.String())
	}
}

func TestCLIHelpAndInvalidFlagsHaveNoActionsOrPasswordReads(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--help"}, {"admin-user", "--help"},
		{"serve", "--storage", "unsupported"}, {"serve", "unexpected"},
		{"serve", "--public-url", "https://name:private@example.test"},
		{"admin-user", "--username", "owner"},
		{"admin-user", "--password-stdin"},
		{"admin-user", "--username", "owner", "--password-stdin=false"},
		{"admin-user", "--password", "must-not-be-accepted"},
		{"unknown"}, {},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			called := false
			reader := &cliCountedReader{reader: strings.NewReader("a long unique passphrase")}
			var output bytes.Buffer
			err := runCLI(context.Background(), args, reader, &output, io.Discard, cliActions{
				serve:     func(context.Context, Config) error { called = true; return nil },
				bootstrap: func(context.Context, Config, string, string, []byte) error { called = true; return nil },
			})
			require.Error(t, err)
			if len(args) > 1 && args[1] == "--help" {
				assert.ErrorIs(t, err, flag.ErrHelp)
			}
			assert.False(t, called)
			assert.Zero(t, reader.read)
			assert.Empty(t, output.String())
		})
	}
}

type cliFailingReader struct{}

func (cliFailingReader) Read([]byte) (int, error) { return 0, errors.New("private-read-error") }

type cliFailingWriter struct{}

func (cliFailingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCLIErrorsCancelWithoutLeakingInputOrStartingServices(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, runCLI(ctx, []string{"serve"}, nil, nil, nil, cliActions{}), context.Canceled)
	missingContext := func() context.Context { return nil }
	assert.ErrorIs(t, runCLI(missingContext(), []string{"serve"}, nil, nil, nil, cliActions{}), ErrInvalidConfig)
	called := false
	err := runCLI(context.Background(), []string{"serve"}, nil, cliFailingWriter{}, nil, cliActions{serve: func(context.Context, Config) error { called = true; return nil }})
	assert.ErrorIs(t, err, io.ErrClosedPipe)
	assert.False(t, called)
	err = runCLI(context.Background(), []string{"admin-user", "--username", "owner", "--password-stdin"}, cliFailingReader{}, nil, nil, cliActions{bootstrap: func(context.Context, Config, string, string, []byte) error { called = true; return nil }})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private-read-error")
	assert.False(t, called)
}
