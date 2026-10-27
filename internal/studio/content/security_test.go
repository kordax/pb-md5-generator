package content

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitSubprocessRejectsUnknownCommandsAndOptions(t *testing.T) {
	backend := &GitBackend{root: t.TempDir(), remote: "origin", timeout: time.Second}
	tests := []struct {
		name      string
		input     string
		arguments []string
	}{
		{name: "missing command"},
		{name: "unknown verb", arguments: []string{"credential", "fill"}},
		{name: "global config", arguments: []string{"config", "--global", "--list"}},
		{name: "hard reset", arguments: []string{"reset", "--hard", strings.Repeat("a", 40)}},
		{name: "extra fetch option", arguments: []string{"fetch", "--no-recurse-submodules", "--", "origin", "--upload-pack=program"}},
		{name: "remote URL", arguments: []string{"fetch", "--no-recurse-submodules", "--", "ssh://host/repo"}},
		{name: "branch option", arguments: []string{"branch", "--", "--track"}},
		{name: "checkout option", arguments: []string{"checkout", "--no-guess", "-b", "--"}},
		{name: "other merge remote", arguments: []string{"merge", "--ff-only", "--no-edit", "--", "other/main"}},
		{name: "force push", arguments: []string{"push", "--force", "--", "origin", "HEAD:refs/heads/main"}},
		{name: "arbitrary push refspec", arguments: []string{"push", "--set-upstream", "--", "origin", ":refs/heads/main"}},
		{name: "unbounded stdin", input: "secret", arguments: []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames"}},
		{name: "hidden staged path", input: ".git/config\x00", arguments: []string{"add", "-f", "--pathspec-from-file=-", "--pathspec-file-nul"}},
		{name: "missing path delimiter", input: "index.md", arguments: []string{"add", "-f", "--pathspec-from-file=-", "--pathspec-file-nul"}},
		{name: "executable config", arguments: []string{"-c", "core.sshCommand=program", "-c", "user.email=a@example.test", "commit", "--no-verify", "--no-gpg-sign", "-m", "docs: update", "--"}},
		{name: "newline actor", arguments: []string{"-c", "user.name=Alice\nBob", "-c", "user.email=a@example.test", "commit", "--no-verify", "--no-gpg-sign", "-m", "docs: update", "--"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := backend.command(context.Background(), test.input, test.arguments...)
			require.ErrorIs(t, err, ErrInvalid)
		})
	}
	assert.True(t, backend.validCommand("docs.json\x00index.md\x00", []string{"add", "-f", "--pathspec-from-file=-", "--pathspec-file-nul"}))
	assert.True(t, backend.validCommand("", []string{"reset", "--keep", strings.Repeat("a", 40)}))
}

func TestPrivateStateDirectoryIsRequiredWithoutChangingPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows access is governed by NTFS ACLs, not FileMode")
	}
	base := t.TempDir()
	state := filepath.Join(base, "state")
	require.NoError(t, os.Mkdir(state, 0o755))
	_, err := New(Config{Root: filepath.Join(base, "content"), StateDir: state, OutputDir: filepath.Join(base, "site"), Initialize: true, Compile: func(context.Context, string, string) error { return nil }})
	require.ErrorIs(t, err, ErrOwnership)
	info, err := os.Stat(state)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	_, err = os.Stat(filepath.Join(state, "owner.json"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}
