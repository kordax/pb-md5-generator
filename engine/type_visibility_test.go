package engine

import (
	"testing"

	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIgnoredTypeReferencesStayOpaqueInLinksAndExamples(t *testing.T) {
	source := "syntax = \"proto3\";\npackage opaque;\n// @ignore\nmessage Hidden { string private_payload = 1; message Nested { string private_nested = 1; } enum Inner { SECRET = 0; } }\n// @ignore\nenum HiddenEnum { PRIVATE_VALUE = 0; }\n// A literal `@ignore` is documentation.\nmessage Visible { Hidden hidden = 1; HiddenEnum hidden_enum = 2; repeated Hidden hidden_list = 3; Hidden.Nested hidden_nested = 4; }\n"
	files, _ := parseExampleSource(t, source)
	ignored := ignoredTypeNames(files)
	for _, name := range []string{"opaque.Hidden", "opaque.Hidden.Nested", "opaque.Hidden.Inner", "opaque.HiddenEnum"} {
		assert.True(t, ignored[name], name)
	}
	assert.False(t, ignored["opaque.Visible"])
	var visible *Message
	for _, file := range files {
		for _, entry := range file.entries {
			if entry.msg != nil && entry.msg.m.GetName() == "Visible" {
				visible = entry.msg
			}
		}
	}
	require.NotNil(t, visible)
	generator := &MDGenerator{ignoredTypes: ignored}
	context := newExampleContext(nil, files)
	for _, field := range visible.fields {
		_, linked := generator.fieldTypeElement(&field).(*md.Link)
		assert.False(t, linked, field.d.GetName())
		value, err := context.fieldValue(field, 0)
		require.NoError(t, err)
		assert.Nil(t, value)
	}
}

func TestVisibilityChecksIndependentCommentChunks(t *testing.T) {
	assert.False(t, ignoredDescriptorComment(nil))
	assert.False(t, ignoredDescriptorComment(&protokit.Comment{Leading: "A literal `@ignore` stays visible."}))
	assert.True(t, ignoredDescriptorComment(&protokit.Comment{Leading: "```text\n@ignore", Trailing: "@ignore"}))
	assert.True(t, ignoredDescriptorComment(&protokit.Comment{Detached: []string{"@code[xml]: <node>@ignore</node>", "@ignore"}}))
	assert.Empty(t, ignoredTypeNames([]ParsedFile{{entries: []Entry{{msg: &Message{}}, {enum: &Enum{}}}}}))
}
