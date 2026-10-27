package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSiteCommandFlags(t *testing.T) {
	cfg, err := parseFlags([]string{"build"})
	require.NoError(t, err)
	assert.Equal(t, commandBuild, cfg.Command)
	assert.Equal(t, "docs.json", cfg.SiteConfig)
	assert.Equal(t, "site", cfg.Output)
	assert.EqualValues(t, 1, cfg.Seed)
	cfg, err = parseFlags([]string{"build", "-config", "project.json", "-output", "dist", "-check", "-seed", "7"})
	require.NoError(t, err)
	assert.Equal(t, "project.json", cfg.SiteConfig)
	assert.Equal(t, "dist", cfg.Output)
	assert.True(t, cfg.Check)
	assert.EqualValues(t, 7, cfg.Seed)
	_, err = parseFlags([]string{"build", "extra"})
	require.ErrorContains(t, err, "unexpected build arguments")
	_, err = parseFlags([]string{"build", "-unknown"})
	require.Error(t, err)
	assert.Zero(t, Run([]string{"build", "-help"}))
}

func TestSiteCommandMixedSources(t *testing.T) {
	project := t.TempDir()
	output := filepath.Join(t.TempDir(), "site")
	config := filepath.Join(project, "docs.json")
	require.NoError(t, os.WriteFile(config, []byte(`{
  "version":1,"title":"Guide and API","pages":[
    {"title":"Guide","source":"index.md","path":"index.html"},
    {"title":"API","source":"api.proto","path":"reference/api.html","kind":"protobuf"}
  ]
}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(project, "index.md"), []byte("# Guide\n\n[API](api.proto#demo.Request)"), 0o600))
	source := `syntax = "proto3";
package demo;
import "not-installed.proto";
// @autocode[json]
message Request {
  repeated string tags = 1;
  map<string, int32> counts = 2;
  oneof choice { string first = 3; int32 second = 4; }
}`
	require.NoError(t, os.WriteFile(filepath.Join(project, "api.proto"), []byte(source), 0o600))
	require.Zero(t, Run([]string{"build", "-config", config, "-o", output}))
	data, err := os.ReadFile(filepath.Join(output, "reference/api.html"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `id="demo.Request"`)
	assert.Contains(t, string(data), `class="language-json"`)
	assert.Contains(t, string(data), "tags")
	require.Zero(t, Run([]string{"build", "-config", config, "-o", output, "-check"}))
	// The same command validates annotations strictly and preserves a previous
	// successful site when an API source becomes invalid.
	before := string(data)
	source = strings.Replace(source, "repeated string tags", "// @doc typo=1\n  repeated string tags", 1)
	require.NoError(t, os.WriteFile(filepath.Join(project, "api.proto"), []byte(source), 0o600))
	assert.Equal(t, 1, Run([]string{"build", "-config", config, "-o", output}))
	data, err = os.ReadFile(filepath.Join(output, "reference/api.html"))
	require.NoError(t, err)
	assert.Equal(t, before, string(data))
	assert.Equal(t, 1, Run([]string{"build", "-config", filepath.Join(project, "missing.json"), "-o", output}))
	_, err = os.Stat(filepath.Join(project, "not-installed.proto"))
	assert.ErrorIs(t, err, fs.ErrNotExist)
}
