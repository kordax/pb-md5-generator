package apiweb

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/stretchr/testify/assert"
)

func TestManagementPathProtectionDeniesPortableCaseAliases(t *testing.T) {
	editor := &auth.User{ID: "editor", Role: auth.RoleEditor}
	admin := &auth.User{ID: "admin", Role: auth.RoleAdmin}
	for _, name := range []string{"docs.json", "DOCS.JSON", "Docs.Json", "DOCſ.JSON", "themes", "THEMES", "Themes/x.json", "Themeſ/x.json", "THEMES/Nested/custom.json"} {
		assert.True(t, reserved(name), name)
		assert.False(t, permitted(editor, auth.ActionRead, name), name)
		assert.False(t, permitted(editor, auth.ActionWrite, name), name)
		assert.True(t, permitted(admin, auth.ActionRead, name), name)
	}
	for _, name := range []string{"other/docs.json", "themes-old/x.json", "public/Themes/x.json", "public/Themeſ/x.json", "docs.json.md"} {
		assert.False(t, reserved(name), name)
		assert.True(t, permitted(editor, auth.ActionRead, name), name)
	}
	admin.Grants = &auth.Grants{Read: []string{"themes"}, Write: []string{}}
	assert.True(t, permitted(admin, auth.ActionRead, "themes/custom.json"))
	assert.False(t, permitted(admin, auth.ActionRead, "Themes/custom.json"), "case-sensitive grants do not gain aliases")
	assert.False(t, permitted(admin, auth.ActionRead, "Themeſ/custom.json"), "Unicode aliases do not expand case-sensitive grants")
}

func TestManagementCaseAliasesCannotCreateReadableOrPublishableFileDrafts(t *testing.T) {
	f := newFixture(t, nil)
	editor := f.createUser(t, "aliaswriter", auth.RoleEditor, nil)
	base := f.current(t, &editor)
	for _, name := range []string{"DOCS.JSON", "Docs.Json", "DOCſ.JSON", "Themes/x.json", "Themeſ/x.json", "THEMES/custom.json"} {
		target := testAPI + "/files/content?path=" + url.QueryEscape(name)
		assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, target, nil, &editor, nil).Code, name)
		assert.Equal(t, http.StatusNotFound, f.request(http.MethodPut, target, []byte("{}"), &editor, map[string]string{"Content-Type": "application/octet-stream", "If-Match": base.Revision}).Code, name)
		assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/documents", map[string]string{"path": name, "title": "Not permitted", "kind": "markdown", "content": "# Attempt", "revision": base.Revision}, &editor, nil).Code, name)
	}
	assert.JSONEq(t, `{"drafts":[]}`, f.request(http.MethodGet, testAPI+"/drafts", nil, &editor, nil).Body.String())
	assert.Equal(t, base.Content, f.current(t, &editor).Content)
}
