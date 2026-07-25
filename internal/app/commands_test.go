package app

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestParseFlagsCommands(t *testing.T) {
	legacy, err := parseFlags([]string{"-d", "proto"})
	require.NoError(t, err)
	assert.Equal(t, commandGenerate, legacy.Command)
	assert.Equal(t, int64(1), legacy.Seed)

	generateConfig, err := parseFlags([]string{
		commandGenerate,
		"-d", "proto",
		"-check",
		"-strict-annotations",
		"-seed", "77",
	})
	require.NoError(t, err)
	assert.Equal(t, commandGenerate, generateConfig.Command)
	assert.True(t, generateConfig.Check)
	assert.True(t, generateConfig.StrictAnnotations)
	assert.Equal(t, int64(77), generateConfig.Seed)

	lintConfig, err := parseFlags([]string{commandLint, "-d", "proto"})
	require.NoError(t, err)
	assert.Equal(t, commandLint, lintConfig.Command)
	assert.True(t, lintConfig.StrictAnnotations)

	_, err = parseFlags([]string{"unknown"})
	assert.ErrorContains(t, err, "unknown command")
	_, err = parseFlags([]string{commandGenerate, "extra"})
	assert.ErrorContains(t, err, "unexpected arguments")
	_, err = parseFlags([]string{commandLint, "-check"})
	assert.ErrorContains(t, err, "does not support -check")
}

func TestLintValidatesWithoutWriting(t *testing.T) {
	deps := testDeps()
	var options generationOptions
	deps.generate = func(_ *pluginpb.CodeGeneratorRequest, received generationOptions) (string, error) {
		options = received
		return "generated", nil
	}
	deps.mkdirAll = func(string, fs.FileMode) error {
		return errors.New("mkdir must not be called")
	}
	deps.writeFile = func(string, []byte, fs.FileMode) error {
		return errors.New("write must not be called")
	}
	deps.readFile = func(string) ([]byte, error) {
		return nil, errors.New("read must not be called")
	}

	err := runWithDeps(Config{
		Command:  commandLint,
		ProtoDir: "proto",
		Files:    "service.proto",
		Seed:     91,
	}, deps)
	require.NoError(t, err)
	assert.True(t, options.strictAnnotations)
	assert.Equal(t, int64(91), options.seed)
}

func TestLintReportsGenerationErrors(t *testing.T) {
	deps := testDeps()
	deps.generate = func(*pluginpb.CodeGeneratorRequest, generationOptions) (string, error) {
		return "", errors.New("bad annotation")
	}

	err := runWithDeps(Config{
		Command:  commandLint,
		ProtoDir: "proto",
		Files:    "service.proto",
	}, deps)
	require.Error(t, err)
	assert.ErrorContains(t, err, "lint failed")
	assert.ErrorContains(t, err, "bad annotation")
}

func TestGenerateCheckDoesNotWrite(t *testing.T) {
	deps := testDeps()
	var checkedPath string
	deps.readFile = func(path string) ([]byte, error) {
		checkedPath = path
		return []byte("generated\n"), nil
	}
	deps.mkdirAll = func(string, fs.FileMode) error {
		return errors.New("mkdir must not be called")
	}
	deps.writeFile = func(string, []byte, fs.FileMode) error {
		return errors.New("write must not be called")
	}

	err := runWithDeps(Config{
		Command:  commandGenerate,
		ProtoDir: "proto",
		Files:    "service.proto",
		Output:   "docs/api",
		Check:    true,
	}, deps)
	require.NoError(t, err)
	assert.Equal(t, "docs/api.md", checkedPath)
}

func TestGenerateCheckReportsOutdatedDocument(t *testing.T) {
	deps := testDeps()
	deps.readFile = func(string) ([]byte, error) {
		return []byte("old\n"), nil
	}

	err := runWithDeps(Config{
		Command:  commandGenerate,
		ProtoDir: "proto",
		Files:    "service.proto",
		Output:   "docs/api",
		Check:    true,
	}, deps)
	require.Error(t, err)
	assert.ErrorContains(t, err, "documentation is out of date")
	assert.ErrorContains(t, err, "docs/api.md")
}
