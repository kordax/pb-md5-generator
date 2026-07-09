package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/pluginpb"
)

func testDeps() appDeps {
	return appDeps{
		checkDependencies: func() error { return nil },
		protoFiles:        func(string) ([]string, error) { return []string{"service.proto"}, nil },
		ensureDirectory:   func(string) error { return nil },
		mkdirAll:          func(string, fs.FileMode) error { return nil },
		removeAll:         func(string) error { return nil },
		requestFromFiles: func(Config, []string) (*pluginpb.CodeGeneratorRequest, error) {
			return &pluginpb.CodeGeneratorRequest{}, nil
		},
		readFile:  func(string) ([]byte, error) { return []byte("prefix"), nil },
		writeFile: func(string, []byte, fs.FileMode) error { return nil },
		generate:  func(*pluginpb.CodeGeneratorRequest) (string, error) { return "generated", nil },
	}
}

func TestParseFlagsAndNormalizeOutput(t *testing.T) {
	cfg, err := parseFlags([]string{"-d", "./proto", "-f", "a.proto;b.proto", "-pbo", "tmp", "-o", "out", "-p", "prefix.md"})
	require.NoError(t, err)

	assert.Equal(t, "./proto", cfg.ProtoDir)
	assert.Equal(t, "a.proto;b.proto", cfg.Files)
	assert.Equal(t, "tmp", cfg.ProtoOut)
	assert.Equal(t, "out", cfg.Output)
	assert.Equal(t, "prefix.md", cfg.PrefixDoc)
	assert.Equal(t, "out.md", normalizedMarkdownOutput("out"))
	assert.Equal(t, "out.md", normalizedMarkdownOutput("out.md"))

	_, err = parseFlags([]string{"-unknown"})
	assert.Error(t, err)
}

func TestRunReturnsFailureForInvalidFlags(t *testing.T) {
	assert.Equal(t, 1, Run([]string{"-unknown"}))
}

func TestResolveProtoFiles(t *testing.T) {
	deps := testDeps()
	files, err := resolveProtoFiles(Config{Files: "a.proto;b.proto"}, deps)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.proto", "b.proto"}, files)

	files, err = resolveProtoFiles(Config{ProtoDir: "proto"}, deps)
	require.NoError(t, err)
	assert.Equal(t, []string{"service.proto"}, files)

	deps.protoFiles = func(string) ([]string, error) { return nil, errors.New("walk failed") }
	_, err = resolveProtoFiles(Config{ProtoDir: "proto"}, deps)
	assert.ErrorContains(t, err, "failed to list .proto files")
}

func TestPrefixValidationAndContent(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "prefix.md")
	require.NoError(t, os.WriteFile(prefix, []byte("hello"), 0o600))

	assert.NoError(t, validatePrefix(""))
	assert.NoError(t, validatePrefix(prefix))
	assert.Error(t, validatePrefix(root))
	assert.Error(t, validatePrefix(filepath.Join(root, "missing.md")))

	content, err := prefixContent("", os.ReadFile)
	require.NoError(t, err)
	assert.Empty(t, content)

	content, err = prefixContent(prefix, os.ReadFile)
	require.NoError(t, err)
	assert.Equal(t, "hello\n\n", content)

	_, err = prefixContent(prefix, func(string) ([]byte, error) { return nil, errors.New("read failed") })
	assert.ErrorContains(t, err, "failed to read prefix")
}

func TestRunWithDepsSuccess(t *testing.T) {
	deps := testDeps()
	root := t.TempDir()
	prefix := filepath.Join(root, "prefix.md")
	require.NoError(t, os.WriteFile(prefix, []byte("prefix"), 0o600))
	var cleaned string
	var writtenPath string
	var writtenContent string
	var writtenMode fs.FileMode

	deps.removeAll = func(path string) error {
		cleaned = path
		return nil
	}
	deps.writeFile = func(path string, content []byte, mode fs.FileMode) error {
		writtenPath = path
		writtenContent = string(content)
		writtenMode = mode
		return nil
	}

	err := runWithDeps(Config{
		ProtoDir:  "./proto",
		Files:     "a.proto;b.proto",
		ProtoOut:  "./tmp",
		Output:    "./out",
		PrefixDoc: prefix,
	}, deps)
	require.NoError(t, err)

	assert.Equal(t, "tmp", cleaned)
	assert.Equal(t, "out.md", writtenPath)
	assert.Equal(t, "prefix\n\ngenerated", writtenContent)
	assert.Equal(t, fs.FileMode(0o600), writtenMode)
}

func TestRunWithDepsFailures(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		deps func(appDeps) appDeps
		want string
	}{
		{
			name: "missing proto dir",
			cfg:  Config{},
			want: "empty proto files",
		},
		{
			name: "dependency check",
			cfg:  Config{ProtoDir: "proto"},
			deps: func(deps appDeps) appDeps {
				deps.checkDependencies = func() error { return errors.New("no protoc") }
				return deps
			},
			want: "no protoc",
		},
		{
			name: "no files",
			cfg:  Config{ProtoDir: "proto"},
			deps: func(deps appDeps) appDeps {
				deps.protoFiles = func(string) ([]string, error) { return nil, nil }
				return deps
			},
			want: "no files specified",
		},
		{
			name: "tmp dir unavailable",
			cfg:  Config{ProtoDir: "proto", Files: "a.proto"},
			deps: func(deps appDeps) appDeps {
				deps.ensureDirectory = func(string) error { return errors.New("exists") }
				return deps
			},
			want: "temporary protobuf directory",
		},
		{
			name: "request failed",
			cfg:  Config{ProtoDir: "proto", Files: "a.proto"},
			deps: func(deps appDeps) appDeps {
				deps.requestFromFiles = func(Config, []string) (*pluginpb.CodeGeneratorRequest, error) {
					return nil, errors.New("bad proto")
				}
				return deps
			},
			want: "failed to generate protobuf request",
		},
		{
			name: "mkdir failed",
			cfg:  Config{ProtoDir: "proto", Files: "a.proto"},
			deps: func(deps appDeps) appDeps {
				deps.mkdirAll = func(string, fs.FileMode) error { return errors.New("permission denied") }
				return deps
			},
			want: "failed to initialize output directory",
		},
		{
			name: "render failed",
			cfg:  Config{ProtoDir: "proto", Files: "a.proto"},
			deps: func(deps appDeps) appDeps {
				deps.generate = func(*pluginpb.CodeGeneratorRequest) (string, error) { return "", errors.New("render") }
				return deps
			},
			want: "failed to generate markdown document",
		},
		{
			name: "write failed",
			cfg:  Config{ProtoDir: "proto", Files: "a.proto"},
			deps: func(deps appDeps) appDeps {
				deps.writeFile = func(string, []byte, fs.FileMode) error { return errors.New("readonly") }
				return deps
			},
			want: "cannot save results",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := testDeps()
			if tt.deps != nil {
				deps = tt.deps(deps)
			}
			err := runWithDeps(tt.cfg, deps)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestRunAndCleanupWrappers(t *testing.T) {
	assert.Error(t, runWithDeps(Config{ProtoDir: "proto", Files: "a.proto"}, appDeps{
		checkDependencies: func() error { return errors.New("dependency") },
	}))

	assert.NotPanics(t, func() {
		cleanup("tmp", func(string) error { return errors.New("cleanup failed") })
	})
}
