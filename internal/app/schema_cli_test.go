package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaCommandFlags(t *testing.T) {
	cfg, err := parseFlags([]string{"schema"})
	require.NoError(t, err)
	assert.Equal(t, commandSchema, cfg.Command)
	assert.Equal(t, "manifest", cfg.SchemaKind)
	assert.Equal(t, "-", cfg.Output)
	assert.False(t, cfg.Check)
	cfg, err = parseFlags([]string{"schema", "-kind", "page", "-output", "page.schema.json", "-check"})
	require.NoError(t, err)
	assert.Equal(t, "page", cfg.SchemaKind)
	assert.Equal(t, "page.schema.json", cfg.Output)
	assert.True(t, cfg.Check)
	for _, args := range [][]string{{"schema", "extra"}, {"schema", "-unknown"}, {"schema", "-check"}, {"schema", "-check=invalid"}, {"schema", "-o"}} {
		_, err := parseFlags(args)
		assert.Error(t, err, "%v", args)
	}
	_, err = parseFlags([]string{"schema", "-help"})
	assert.ErrorIs(t, err, flag.ErrHelp)
}

func TestSchemaCommandStdoutIsOnlyJSON(t *testing.T) {
	for _, kind := range []string{"manifest", "project", "page"} {
		t.Run(kind, func(t *testing.T) {
			args := []string{"schema"}
			if kind != "manifest" {
				args = append(args, "-kind", kind)
			}
			stdout, stderr, err := appCommandOutput(t, args...)
			require.NoError(t, err, "%s", stderr)
			assert.Empty(t, stderr)
			expected, err := document.JSONSchema(kind)
			require.NoError(t, err)
			assert.Equal(t, string(expected), stdout)
			decoder := json.NewDecoder(strings.NewReader(stdout))
			var schema map[string]any
			require.NoError(t, decoder.Decode(&schema))
			assert.Equal(t, "https://json-schema.org/draft/2020-12/schema", schema["$schema"])
			assert.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
		})
	}
	stdout, stderr, err := appCommandOutput(t, "schema", "-kind", "unknown")
	require.Error(t, err)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "schema")
}

func TestRootCommandHelpListsSiteAndSchema(t *testing.T) {
	stdout, stderr, err := appCommandOutput(t, "-help")
	require.NoError(t, err)
	assert.Empty(t, stdout)
	for _, command := range []string{"generate", "lint", "build", "schema"} {
		assert.Contains(t, stderr, "pb-md5-generator "+command)
	}
	assert.Contains(t, stderr, "command-specific flags")
}

func TestSchemaCommandFileAndCheckErrors(t *testing.T) {
	output := filepath.Join(t.TempDir(), "nested", "page.schema.json")
	cfg := Config{Command: commandSchema, SchemaKind: "page", Output: output}
	var stdout bytes.Buffer
	require.NoError(t, writeSchemaTo(cfg, &stdout))
	assert.Empty(t, stdout.String())
	expected, err := document.JSONSchema("page")
	require.NoError(t, err)
	actual, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, string(expected), string(actual))
	old := time.Unix(1000, 0)
	require.NoError(t, os.Chtimes(output, old, old))
	cfg.Check = true
	require.NoError(t, writeSchemaTo(cfg, &stdout))
	info, err := os.Stat(output)
	require.NoError(t, err)
	assert.Equal(t, old, info.ModTime(), "check must not rewrite a current file")
	mismatch := []byte("{\"outdated\":true}\n")
	require.NoError(t, os.WriteFile(output, mismatch, 0o600))
	require.ErrorContains(t, writeSchemaTo(cfg, &stdout), "out of date")
	actual, err = os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, mismatch, actual, "check must preserve an outdated file")
	cfg.Output = filepath.Join(t.TempDir(), "missing.json")
	require.ErrorContains(t, writeSchemaTo(cfg, &stdout), "out of date")
	_, err = os.Stat(cfg.Output)
	assert.ErrorIs(t, err, os.ErrNotExist)

	cfg.Check = false
	cfg.Output = t.TempDir()
	require.ErrorContains(t, writeSchemaTo(cfg, &stdout), "cannot save results")
	parentFile := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(parentFile, nil, 0o600))
	cfg.Output = filepath.Join(parentFile, "schema.json")
	require.ErrorContains(t, writeSchemaTo(cfg, &stdout), "initialize")
	for _, invalid := range []Config{{SchemaKind: "unknown", Output: "-"}, {SchemaKind: "manifest", Output: "-", Check: true}, {SchemaKind: "manifest"}} {
		assert.Error(t, writeSchemaTo(invalid, &stdout))
	}
	assert.Empty(t, stdout.String())
	assert.ErrorIs(t, writeSchemaTo(Config{SchemaKind: "manifest", Output: "-"}, schemaFailWriter{}), io.ErrClosedPipe)
}

type schemaFailWriter struct{}

func (schemaFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// A separate test process exercises Run's real stdout/stderr without changing
// the parent process's globals or allowing the test runner to append PASS text.
func TestAppCommandProcess(t *testing.T) {
	if os.Getenv("PBMD5_TEST_COMMAND") != "1" {
		return
	}
	for index, arg := range os.Args {
		if arg == "--" {
			os.Exit(Run(os.Args[index+1:]))
		}
	}
	os.Exit(2)
}

func appCommandOutput(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestAppCommandProcess$", "--"}, args...)...)
	command.Env = append(os.Environ(), "PBMD5_TEST_COMMAND=1")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("CLI test process exceeded its timeout")
	}
	return stdout.String(), stderr.String(), err
}
