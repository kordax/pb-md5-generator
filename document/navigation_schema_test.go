package document_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func navigationManifestJSON(tree string) []byte {
	return []byte(`{"version":1,"title":"Docs","pages":[{"id":"home","title":"Home","source":"index.md","path":"index.html"}],"navigation":{"showTOC":true,"showPreviousNext":true,"stickyHeader":true,"tree":` + tree + `}}`)
}

func TestNavigationSchemasValidateSourceAndPortableProjectTrees(t *testing.T) {
	sourceSchema := compileDocumentationSchema(t, "manifest")
	projectSchema := compileDocumentationSchema(t, "project")
	for _, tree := range []string{
		`null`,
		`{"items":[]}`,
		`{"unlistedPages":"omit","items":[]}`,
		`{"items":[{"kind":"page","pageId":"home","title":""}]}`,
		`{"unlistedPages":"append","items":[{"kind":"group","id":"guides","title":"Guides","description":"Line one\nLine two\tplain text","collapsed":false,"children":[{"kind":"page","pageId":"home","title":"Start here"}]}]}`,
		`{"items":[{"kind":"group","id":"guides","title":"Guides","collapsed":null,"children":[{"kind":"page","pageId":"home"}]}]}`,
	} {
		t.Run(tree, func(t *testing.T) {
			source := navigationManifestJSON(tree)
			require.NoError(t, sourceSchema.Validate(decodeSchemaData(t, source)))
			manifest, err := site.DecodeManifest(source)
			require.NoError(t, err)
			project := site.ProjectModel(manifest)
			encoded, err := site.EncodeProjectModel(context.Background(), project)
			require.NoError(t, err)
			require.NoError(t, projectSchema.Validate(decodeSchemaData(t, encoded)))
			var decoded document.Project
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			if tree == "null" {
				assert.Nil(t, decoded.Navigation.Tree)
			} else {
				require.NotNil(t, decoded.Navigation.Tree)
				require.NotNil(t, decoded.Navigation.Tree.Items)
			}
		})
	}
}

func TestNavigationSchemasAndCompilerRejectWrongKindFieldsEvenWhenEmpty(t *testing.T) {
	sourceSchema := compileDocumentationSchema(t, "manifest")
	for _, tree := range []string{
		`{}`,
		`{"items":null}`,
		`{"unlistedPages":"","items":[]}`,
		`{"unlistedPages":null,"items":[]}`,
		`{"unlistedPages":"redirect","items":[]}`,
		`{"items":[],"url":"https://example.invalid"}`,
		`{"items":[{"kind":"page","pageId":"home","id":""}]}`,
		`{"items":[{"kind":"page","pageId":"home","description":""}]}`,
		`{"items":[{"kind":"page","pageId":"home","collapsed":false}]}`,
		`{"items":[{"kind":"page","pageId":"home","children":[]}]}`,
		`{"items":[{"kind":"page","pageId":"home","children":null}]}`,
		`{"items":[{"kind":"page","pageId":"home","title":null}]}`,
		`{"items":[{"kind":"page","pageId":"home","url":"javascript:alert(1)"}]}`,
		`{"items":[{"kind":"external","pageId":"home"}]}`,
		`{"items":[{"kind":"page"}]}`,
		`{"items":[{"kind":"page","pageId":12}]}`,
		`{"items":[{"kind":"group","id":"group","title":"Group","pageId":"","children":[{"kind":"page","pageId":"home"}]}]}`,
		`{"items":[{"kind":"group","id":"group","title":"Group"}]}`,
		`{"items":[{"kind":"group","id":"group","title":"Group","children":[]}]}`,
		`{"items":[{"kind":"group","id":"Group.Bad","title":"Group","children":[{"kind":"page","pageId":"home"}]}]}`,
		`{"items":[{"kind":"group","id":"group","title":null,"children":[{"kind":"page","pageId":"home"}]}]}`,
		`{"items":[{"kind":"group","id":"group","title":"Group","collapsed":"yes","children":[{"kind":"page","pageId":"home"}]}]}`,
	} {
		t.Run(tree, func(t *testing.T) {
			source := navigationManifestJSON(tree)
			assert.Error(t, sourceSchema.Validate(decodeSchemaData(t, source)))
			_, err := site.DecodeManifest(source)
			assert.Error(t, err)
		})
	}
}

func TestNavigationDraftSchemaLeavesReferenceAndByteChecksToTheCompiler(t *testing.T) {
	schema := compileDocumentationSchema(t, "manifest")
	for _, tree := range []string{
		`{"items":[{"kind":"page","pageId":"missing-page"}]}`,
		`{"items":[{"kind":"page","pageId":"home"},{"kind":"page","pageId":"home"}]}`,
		`{"items":[{"kind":"group","id":"group","title":"` + strings.Repeat("界", 100) + `","children":[{"kind":"page","pageId":"home"}]}]}`,
	} {
		source := navigationManifestJSON(tree)
		require.NoError(t, schema.Validate(decodeSchemaData(t, source)), "v1 remains a structural draft, not a claim of complete compiler validation")
		_, err := site.DecodeManifest(source)
		assert.Error(t, err)
	}
}

func TestNavigationTypedDecodersRejectTrailingAndNonobjectInput(t *testing.T) {
	for _, data := range []string{`null`, `[]`, `{`, `{"items":[]} {}`, `{"items":[]} trailing`} {
		var tree document.NavigationTree
		assert.Error(t, json.Unmarshal([]byte(data), &tree), data)
	}
	for _, data := range []string{`null`, `[]`, `{`, `{"kind":"page","pageId":"home"} {}`} {
		var node document.NavigationNode
		assert.Error(t, json.Unmarshal([]byte(data), &node), data)
	}
}
