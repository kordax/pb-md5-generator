package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func testDeps() appDeps {
	return appDeps{
		protoFiles: func(string) ([]string, error) { return []string{"service.proto"}, nil },
		mkdirAll:   func(string, fs.FileMode) error { return nil },
		requestFromFiles: func(Config, []string) (*pluginpb.CodeGeneratorRequest, error) {
			return &pluginpb.CodeGeneratorRequest{}, nil
		},
		readFile:  func(string) ([]byte, error) { return []byte("prefix"), nil },
		writeFile: func(string, []byte, fs.FileMode) error { return nil },
		generate: func(*pluginpb.CodeGeneratorRequest, engine.GeneratorStyle, string, []string) (string, error) {
			return "generated", nil
		},
	}
}

func TestParseFlagsAndNormalizeOutput(t *testing.T) {
	cfg, err := parseFlags([]string{
		"-d", "./proto",
		"-I", "./vendor-one",
		"-proto-path", "./vendor-two",
		"-f", "a.proto;b.proto",
		"-o", "out",
		"-p", "prefix.md",
		"-split-by-package",
		"-style-table-identifiers", "bold",
		"-style-heading-identifiers", "plain",
	})
	require.NoError(t, err)

	assert.Equal(t, "./proto", cfg.ProtoDir)
	assert.Equal(t, []string{"./vendor-one", "./vendor-two"}, cfg.ProtoPaths)
	assert.Equal(t, "a.proto;b.proto", cfg.Files)
	assert.Equal(t, "out", cfg.Output)
	assert.Equal(t, "prefix.md", cfg.PrefixDoc)
	assert.True(t, cfg.SplitByPackage)
	assert.Equal(t, "bold", cfg.TableIdentifierStyle)
	assert.Equal(t, "plain", cfg.HeadingIdentifierStyle)
	assert.Equal(t, "out.md", normalizedMarkdownOutput("out"))
	assert.Equal(t, "out.md", normalizedMarkdownOutput("out.md"))

	_, err = parseFlags([]string{"-unknown"})
	assert.Error(t, err)
}

func TestRunReturnsFailureForInvalidFlags(t *testing.T) {
	assert.Equal(t, 1, Run([]string{"-unknown"}))
	assert.Equal(t, 0, Run([]string{"-help"}))
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
	var writtenPath string
	var writtenContent string
	var writtenMode fs.FileMode
	var generatedStyle engine.GeneratorStyle

	deps.writeFile = func(path string, content []byte, mode fs.FileMode) error {
		writtenPath = path
		writtenContent = string(content)
		writtenMode = mode
		return nil
	}
	deps.generate = func(_ *pluginpb.CodeGeneratorRequest, style engine.GeneratorStyle, packageName string, knownPackages []string) (string, error) {
		assert.Empty(t, packageName)
		assert.Nil(t, knownPackages)
		generatedStyle = style
		return "generated", nil
	}

	err := runWithDeps(Config{
		ProtoDir:               "./proto",
		Files:                  "a.proto;b.proto",
		Output:                 "./out",
		PrefixDoc:              prefix,
		TableIdentifierStyle:   "plain",
		HeadingIdentifierStyle: "bold-code",
	}, deps)
	require.NoError(t, err)

	assert.Equal(t, "out.md", writtenPath)
	assert.Equal(t, "prefix\n\ngenerated\n", writtenContent)
	assert.Equal(t, fs.FileMode(0o600), writtenMode)
	assert.Equal(t, engine.IdentifierStylePlain, generatedStyle.TableIdentifiers)
	assert.Equal(t, engine.IdentifierStyleBoldCode, generatedStyle.HeadingIdentifiers)
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
			name: "no files",
			cfg:  Config{ProtoDir: "proto"},
			deps: func(deps appDeps) appDeps {
				deps.protoFiles = func(string) ([]string, error) { return nil, nil }
				return deps
			},
			want: "no files specified",
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
			want: "failed to initialize markdown output directory",
		},
		{
			name: "render failed",
			cfg:  Config{ProtoDir: "proto", Files: "a.proto"},
			deps: func(deps appDeps) appDeps {
				deps.generate = func(*pluginpb.CodeGeneratorRequest, engine.GeneratorStyle, string, []string) (string, error) {
					return "", errors.New("render")
				}
				return deps
			},
			want: "failed to generate markdown document",
		},
		{
			name: "invalid table identifier style",
			cfg:  Config{ProtoDir: "proto", Files: "a.proto", TableIdentifierStyle: "weird", HeadingIdentifierStyle: "code"},
			want: "invalid table identifier style",
		},
		{
			name: "invalid heading identifier style",
			cfg:  Config{ProtoDir: "proto", Files: "a.proto", TableIdentifierStyle: "code", HeadingIdentifierStyle: "weird"},
			want: "invalid heading identifier style",
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

func TestSplitRequestByPackage(t *testing.T) {
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"z.proto", "nested/b.proto", "a.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			{Name: goproto.String("a.proto"), Package: goproto.String("alpha.v1")},
			{Name: goproto.String("nested/b.proto"), Package: goproto.String("beta")},
			{Name: goproto.String("z.proto"), Package: goproto.String("alpha.v1")},
		},
	}

	packages, err := splitRequestByPackage(request)
	require.NoError(t, err)
	require.Len(t, packages, 2)
	assert.Equal(t, "alpha.v1", packages[0].name)
	assert.Equal(t, []string{"a.proto", "z.proto"}, packages[0].request.GetFileToGenerate())
	assert.Equal(t, "beta", packages[1].name)
	assert.Equal(t, []string{"nested/b.proto"}, packages[1].request.GetFileToGenerate())

	_, err = splitRequestByPackage(&pluginpb.CodeGeneratorRequest{FileToGenerate: []string{"missing.proto"}})
	assert.ErrorContains(t, err, "descriptor for source file")
}

func TestWritePackageDocuments(t *testing.T) {
	deps := testDeps()
	written := make(map[string]string)
	deps.writeFile = func(path string, content []byte, _ fs.FileMode) error {
		written[path] = string(content)
		return nil
	}
	deps.generate = func(_ *pluginpb.CodeGeneratorRequest, _ engine.GeneratorStyle, packageName string, knownPackages []string) (string, error) {
		assert.ElementsMatch(t, []string{"alpha.v1", "beta"}, knownPackages)
		return "generated " + packageName, nil
	}

	root := filepath.Join(t.TempDir(), "docs")
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"alpha.proto", "beta.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			{Name: goproto.String("alpha.proto"), Package: goproto.String("alpha.v1")},
			{Name: goproto.String("beta.proto"), Package: goproto.String("beta")},
		},
	}
	require.NoError(t, writePackageDocuments(
		Config{Output: root},
		request,
		"prefix\n\n",
		engine.DefaultGeneratorStyle(),
		deps,
	))

	assert.Equal(t, "prefix\n\ngenerated alpha.v1\n", written[filepath.Join(root, "alpha", "v1", "README.md")])
	assert.Equal(t, "prefix\n\ngenerated beta\n", written[filepath.Join(root, "beta", "README.md")])
	index := written[filepath.Join(root, "README.md")]
	assert.Contains(t, index, "[`alpha.v1`](alpha/v1/README.md)")
	assert.Contains(t, index, "[`beta`](beta/README.md)")
	assert.True(t, strings.HasSuffix(index, "\n"))
}

func TestPackageDirectory(t *testing.T) {
	path, err := packageDirectory("example.auth.v2")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("example", "auth", "v2"), path)

	path, err = packageDirectory("")
	require.NoError(t, err)
	assert.Equal(t, "_default", path)

	_, err = packageDirectory("bad..package")
	assert.Error(t, err)
}
