package document_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceSchemaOptionalNullGroupsMatchCompilerDefaults(t *testing.T) {
	source := []byte(`{"version":1,"title":"Docs","pages":[{"id":"home","title":"Home","source":"index.md","path":"index.html"}],"themeId":"paper","theme":null,"navigation":null,"editor":null,"api":null,"publishing":null}`)
	sourceSchema := compileDocumentationSchema(t, "manifest")
	require.NoError(t, sourceSchema.Validate(decodeSchemaData(t, source)))
	manifest, err := site.DecodeManifest(source)
	require.NoError(t, err)
	assert.Nil(t, manifest.Theme)
	assert.Nil(t, manifest.Navigation)
	assert.Nil(t, manifest.Editor)
	assert.Nil(t, manifest.API)
	assert.Nil(t, manifest.Publishing)
	assert.Equal(t, "paper", manifest.Appearance().Theme.ID)
	project := site.ProjectModel(manifest)
	projectSchema := compileDocumentationSchema(t, "project")
	artifact := modelSchemaData(t, project).(map[string]any)
	require.NoError(t, projectSchema.Validate(artifact))
	for _, name := range []string{"theme", "navigation", "editor", "api", "publishing"} {
		assert.NotContains(t, artifact, name)
		copy := modelSchemaData(t, project).(map[string]any)
		copy[name] = nil
		assert.Error(t, projectSchema.Validate(copy), "compiled objects omit unset fields instead of emitting null")
	}
	// The nullable source rule belongs only to optional pointer groups and
	// palettes; required fields and collection types retain their contracts.
	for _, name := range []string{"version", "title", "pages", "assets"} {
		value := decodeSchemaData(t, source).(map[string]any)
		value[name] = nil
		assert.Error(t, sourceSchema.Validate(value))
	}
	theme := site.DefaultTheme()
	theme.ID, theme.Name = "custom", "Custom"
	themeData := modelSchemaData(t, theme).(map[string]any)
	themeData["light"], themeData["dark"] = nil, nil
	value := decodeSchemaData(t, source).(map[string]any)
	value["themeId"], value["theme"] = theme.ID, themeData
	require.NoError(t, sourceSchema.Validate(value))
	data, err := json.Marshal(value)
	require.NoError(t, err)
	manifest, err = site.DecodeManifest(data)
	require.NoError(t, err)
	require.NotNil(t, manifest.Theme)
	assert.Nil(t, manifest.Theme.Light)
	assert.Nil(t, manifest.Theme.Dark)
	require.NoError(t, projectSchema.Validate(modelSchemaData(t, site.ProjectModel(manifest))))
	_, err = site.DecodeManifest([]byte(strings.Repeat(" ", (1<<20)+1)))
	require.ErrorContains(t, err, "manifest exceeds")
	_, err = site.DecodeManifest([]byte{0xff})
	require.ErrorContains(t, err, "not valid UTF-8")
}
