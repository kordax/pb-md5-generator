package content

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type localBackend struct{}

func NewLocalBackend() Backend { return localBackend{} }

func (localBackend) Status(context.Context) (SyncStatus, error) {
	return SyncStatus{Backend: "local", State: "local"}, nil
}

func (localBackend) Branches(context.Context) ([]Branch, error) { return []Branch{}, nil }

func (localBackend) CreateBranch(context.Context, string) error { return ErrUnsupported }

func (localBackend) Checkout(context.Context, string) error { return ErrUnsupported }

func (localBackend) Fetch(context.Context) error { return ErrUnsupported }

func (localBackend) Pull(context.Context) error { return ErrUnsupported }

func (localBackend) RestoreHead(context.Context, string) error { return ErrUnsupported }

func (localBackend) Commit(context.Context, []string, Actor, string) (string, error) { return "", nil }

func (localBackend) Push(context.Context) error { return ErrUnsupported }

type GitConfig struct {
	Root    string
	Remote  string
	Timeout time.Duration
}

type GitBackend struct {
	root    string
	remote  string
	timeout time.Duration
}

type cappedOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (output *cappedOutput) Write(data []byte) (int, error) {
	if len(data) > output.limit-output.buffer.Len() {
		return 0, ErrLimit
	}
	return output.buffer.Write(data)
}

func (output *cappedOutput) Bytes() []byte { return output.buffer.Bytes() }

func NewGitBackend(config GitConfig) (*GitBackend, error) {
	if config.Root == "" {
		return nil, ErrInvalid
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if err := rejectDevelopmentCheckout(root); err != nil {
		return nil, err
	}
	if config.Remote == "" {
		config.Remote = "origin"
	}
	if len(config.Remote) > 64 || config.Remote[0] == '-' || strings.IndexFunc(config.Remote, func(char rune) bool {
		return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-", char)
	}) >= 0 {
		return nil, ErrInvalid
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Timeout < time.Second || config.Timeout > 2*time.Minute {
		return nil, ErrInvalid
	}
	backend := &GitBackend{root: root, remote: config.Remote, timeout: config.Timeout}
	ctx := context.Background()
	data, err := backend.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil || filepath.Clean(strings.TrimSpace(string(data))) != root {
		return nil, fmt.Errorf("%w: Git storage requires its own managed working tree", ErrOwnership)
	}
	if err := backend.safeConfig(ctx); err != nil {
		return nil, err
	}
	return backend, nil
}

func (g *GitBackend) ManagedRoot() string { return g.root }

func gitEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+8)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "GIT_") || key == "SSH_ASKPASS" || key == "LC_ALL" {
			continue
		}
		environment = append(environment, value)
	}
	return append(environment, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_LITERAL_PATHSPECS=1", "LC_ALL=C", "GIT_SSH_COMMAND=ssh -oBatchMode=yes -oConnectTimeout=10 -oStrictHostKeyChecking=yes")
}

func (g *GitBackend) command(ctx context.Context, input string, arguments ...string) ([]byte, error) {
	if !g.validCommand(input, arguments) {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	flags := []string{"-C", g.root, "-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "commit.gpgSign=false", "-c", "tag.gpgSign=false", "-c", "credential.helper=", "-c", "protocol.ext.allow=never", "-c", "protocol.file.allow=always", "-c", "core.pager=cat"}
	command := exec.CommandContext(ctx, "git")
	command.Args = append(command.Args, flags...)
	command.Args = append(command.Args, arguments...)
	command.Env = gitEnvironment()
	command.WaitDelay = 2 * time.Second
	output := &cappedOutput{limit: 2 << 20}
	command.Stdout, command.Stderr = output, io.Discard
	if input != "" {
		command.Stdin = strings.NewReader(input)
	}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return output.Bytes(), err
	}
	return output.Bytes(), nil
}

func (g *GitBackend) run(ctx context.Context, arguments ...string) ([]byte, error) {
	return g.command(ctx, "", arguments...)
}

func (g *GitBackend) statusData(ctx context.Context) ([]byte, error) {
	data, err := g.run(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	var result bytes.Buffer
	for _, record := range bytes.Split(data, []byte{0}) {
		if len(record) == 0 || (len(record) >= 4 && string(record[3:]) == ".studio-lock") {
			continue
		}
		_, _ = result.Write(record)
		_ = result.WriteByte(0)
	}
	return result.Bytes(), nil
}

func gitError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("git %s failed; local content is retained", operation)
}

func (g *GitBackend) safeConfig(ctx context.Context) error {
	data, err := g.run(ctx, "config", "--local", "--no-includes", "--name-only", "--list")
	if err != nil {
		return gitError("configuration validation", err)
	}
	for _, key := range strings.Split(strings.ToLower(string(data)), "\n") {
		if strings.HasPrefix(key, "filter.") || strings.HasPrefix(key, "include.") || strings.HasPrefix(key, "includeif.") || key == "core.sshcommand" || key == "core.attributesfile" || key == "extensions.worktreeconfig" || strings.HasSuffix(key, ".uploadpack") || strings.HasSuffix(key, ".receivepack") || strings.HasSuffix(key, ".proxy") || strings.HasSuffix(key, ".vcs") {
			return fmt.Errorf("%w: executable Git configuration is unsupported", ErrInvalid)
		}
	}
	return nil
}

func (g *GitBackend) safeRemote(ctx context.Context) error {
	if err := g.safeConfig(ctx); err != nil {
		return err
	}
	data, err := g.run(ctx, "remote", "get-url", "--all", g.remote)
	if err != nil {
		return gitError("remote validation", err)
	}
	remotes := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(remotes) != 1 || !allowedRemote(remotes[0]) {
		return fmt.Errorf("%w: configure one local or SSH remote without embedded credentials", ErrInvalid)
	}
	data, err = g.run(ctx, "remote", "get-url", "--push", "--all", g.remote)
	if err != nil {
		return gitError("push remote validation", err)
	}
	remotes = strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(remotes) != 1 || !allowedRemote(remotes[0]) {
		return fmt.Errorf("%w: configure one local or SSH remote without embedded credentials", ErrInvalid)
	}
	return nil
}

func allowedRemote(value string) bool {
	if value == "" || strings.IndexFunc(value, unicode.IsControl) >= 0 || strings.HasPrefix(value, "-") {
		return false
	}
	if filepath.IsAbs(value) {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil {
		transport, _, uri := strings.Cut(value, "://")
		if uri && !strings.ContainsRune(transport, '@') {
			return false
		}
	}
	if err == nil && parsed.Scheme != "" {
		if parsed.Scheme == "file" {
			return parsed.Host == "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && filepath.IsAbs(parsed.Path)
		}
		if parsed.Scheme != "ssh" || parsed.Hostname() == "" || strings.HasPrefix(parsed.Hostname(), "-") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return false
		}
		if parsed.User != nil {
			if _, password := parsed.User.Password(); password {
				return false
			}
		}
		return true
	}
	left, right, ok := strings.Cut(value, ":")
	if !ok || right == "" || strings.ContainsAny(left, "/ \\?&#") || strings.ContainsAny(right, "\n\r") {
		return false
	}
	_, host, user := strings.Cut(left, "@")
	if !user {
		host = left
	}
	return host != "" && !strings.HasPrefix(host, "-")
}

func (g *GitBackend) branchName(ctx context.Context, name string) error {
	if name == "" || len(name) > 128 || !utf8.ValidString(name) || strings.HasPrefix(name, "-") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return ErrInvalid
	}
	if _, err := g.run(ctx, "check-ref-format", "--branch", name); err != nil {
		return ErrInvalid
	}
	return nil
}

func (g *GitBackend) Status(ctx context.Context) (SyncStatus, error) {
	branch, err := g.run(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return SyncStatus{}, fmt.Errorf("%w: detached Git HEAD is unsupported", ErrConflict)
	}
	result := SyncStatus{Backend: "git", State: "clean", Branch: strings.TrimSpace(string(branch))}
	head, err := g.run(ctx, "rev-parse", "--verify", "HEAD")
	if err == nil {
		result.Head = strings.TrimSpace(string(head))
	}
	data, err := g.statusData(ctx)
	if err != nil {
		return result, gitError("status", err)
	}
	result.Dirty = len(data) != 0
	if result.Dirty {
		result.State = "modified"
	}
	counts, err := g.run(ctx, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err == nil {
		fields := strings.Fields(string(counts))
		if len(fields) == 2 {
			result.Ahead, _ = strconv.Atoi(fields[0])
			result.Behind, _ = strconv.Atoi(fields[1])
		}
	}
	if result.Ahead > 0 || (err != nil && result.Head != "" && !result.Dirty) {
		result.State, result.Retryable = "pending", true
	} else if result.Behind > 0 && !result.Dirty {
		result.State = "behind"
	}
	return result, nil
}

func (g *GitBackend) Branches(ctx context.Context) ([]Branch, error) {
	status, err := g.Status(ctx)
	if err != nil {
		return nil, err
	}
	data, err := g.run(ctx, "for-each-ref", "--format=%(refname:short)%00%(objectname)", "refs/heads/")
	if err != nil {
		return nil, gitError("branch listing", err)
	}
	result := []Branch{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		name, head, ok := strings.Cut(line, "\x00")
		if !ok || len(result) >= 256 {
			return nil, ErrLimit
		}
		result = append(result, Branch{Name: name, Head: head, Current: name == status.Branch})
	}
	return result, nil
}

func (g *GitBackend) clean(ctx context.Context) error {
	if err := g.safeConfig(ctx); err != nil {
		return err
	}
	status, err := g.Status(ctx)
	if err != nil {
		return err
	}
	if status.Dirty {
		return fmt.Errorf("%w: save or resolve working tree changes before synchronizing", ErrConflict)
	}
	return nil
}

func (g *GitBackend) CreateBranch(ctx context.Context, name string) error {
	if err := g.branchName(ctx, name); err != nil {
		return err
	}
	if err := g.clean(ctx); err != nil {
		return err
	}
	if _, err := g.run(ctx, "branch", "--", name); err != nil {
		return gitError("branch creation", err)
	}
	return nil
}

func (g *GitBackend) Checkout(ctx context.Context, name string) error {
	if err := g.branchName(ctx, name); err != nil {
		return err
	}
	if err := g.clean(ctx); err != nil {
		return err
	}
	if _, err := g.run(ctx, "checkout", "--no-guess", name, "--"); err != nil {
		return gitError("branch selection", err)
	}
	return nil
}

func (g *GitBackend) Fetch(ctx context.Context) error {
	if err := g.safeRemote(ctx); err != nil {
		return err
	}
	if _, err := g.run(ctx, "fetch", "--no-recurse-submodules", "--", g.remote); err != nil {
		return gitError("fetch", err)
	}
	return nil
}

func (g *GitBackend) Pull(ctx context.Context) error {
	if err := g.clean(ctx); err != nil {
		return err
	}
	if err := g.Fetch(ctx); err != nil {
		return err
	}
	status, err := g.Status(ctx)
	if err != nil {
		return err
	}
	if err := g.branchName(ctx, status.Branch); err != nil {
		return err
	}
	if _, err := g.run(ctx, "merge", "--ff-only", "--no-edit", "--", g.remote+"/"+status.Branch); err != nil {
		return fmt.Errorf("%w: Git fast-forward pull failed", ErrConflict)
	}
	return nil
}

func (g *GitBackend) RestoreHead(ctx context.Context, head string) error {
	if len(head) != 40 && len(head) != 64 {
		return ErrInvalid
	}
	if _, err := hexDecode(head); err != nil {
		return ErrInvalid
	}
	if err := g.clean(ctx); err != nil {
		return err
	}
	if _, err := g.run(ctx, "reset", "--keep", head); err != nil {
		return gitError("candidate rollback", err)
	}
	return nil
}

func hexDecode(value string) ([]byte, error) {
	return hex.DecodeString(value)
}

func (g *GitBackend) Commit(ctx context.Context, paths []string, actor Actor, message string) (string, error) {
	if err := g.safeConfig(ctx); err != nil {
		return "", err
	}
	actor, err := normalizeActor(actor)
	if err != nil {
		return "", err
	}
	if _, err := mail.ParseAddress(actor.Email); err != nil {
		return "", ErrInvalid
	}
	if message == "" {
		message = "docs: update documentation"
	}
	if len(message) > 256 || !utf8.ValidString(message) || strings.IndexFunc(message, unicode.IsControl) >= 0 {
		return "", ErrInvalid
	}
	allowed := make(map[string]bool, len(paths))
	for _, name := range paths {
		if !allowedFile(name) {
			return "", ErrInvalid
		}
		allowed[name] = true
	}
	data, err := g.statusData(ctx)
	if err != nil {
		return "", gitError("status", err)
	}
	for _, record := range bytes.Split(data, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		if len(record) < 4 || !allowed[string(record[3:])] {
			return "", fmt.Errorf("%w: Git contains unrelated changes", ErrConflict)
		}
	}
	if len(data) == 0 {
		status, err := g.Status(ctx)
		return status.Head, err
	}
	input := strings.Join(paths, "\x00") + "\x00"
	if _, err := g.command(ctx, input, "add", "-f", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
		return "", gitError("staging", err)
	}
	staged, err := g.run(ctx, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return "", gitError("staged path validation", err)
	}
	for _, name := range bytes.Split(staged, []byte{0}) {
		if len(name) != 0 && !allowed[string(name)] {
			return "", ErrConflict
		}
	}
	if _, err := g.run(ctx, "-c", "user.name="+actor.Name, "-c", "user.email="+actor.Email, "commit", "--no-verify", "--no-gpg-sign", "-m", message, "--"); err != nil {
		return "", gitError("commit", err)
	}
	status, err := g.Status(ctx)
	return status.Head, err
}

func (g *GitBackend) Push(ctx context.Context) error {
	if err := g.safeRemote(ctx); err != nil {
		return err
	}
	if err := g.clean(ctx); err != nil {
		return err
	}
	status, err := g.Status(ctx)
	if err != nil {
		return err
	}
	if err := g.branchName(ctx, status.Branch); err != nil {
		return err
	}
	if _, err := g.run(ctx, "push", "--set-upstream", "--", g.remote, "HEAD:refs/heads/"+status.Branch); err != nil {
		return gitError("push", err)
	}
	return nil
}
