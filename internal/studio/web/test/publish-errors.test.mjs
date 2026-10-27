import test from "node:test";
import assert from "node:assert/strict";
import { APIError, Workspace, mergeSettings } from "../assets/core.mjs";
import { mergeThemeFields } from "../assets/themes.mjs";

test("failed publication adopts the persisted draft CAS and preserves edits made while saving", async () => {
  const sent = []; let rejectSave;
  const failure = new Promise((_resolve, reject) => { rejectSave = reject; });
  const workspace = new Workspace({ request: async (_path, options) => {
    if (options?.method !== "PUT") return { id: "guide", path: "guide.md", title: "Guide", kind: "markdown", revision: "project-1", content: "original" };
    sent.push(options.body);
    if (sent.length === 1) return failure;
    return { revision: "project-1", published: false, draft: { id: "saved-draft", revision: "draft-2", baseRevision: "project-1" } };
  } });
  await workspace.open("guide"); workspace.edit("invalid source");
  const saving = workspace.save(true); workspace.edit("fixed source typed while publishing");
  rejectSave(new APIError("Publication failed validation", 422, "invalid_content", {
    draft: { id: "saved-draft", revision: "draft-1", baseRevision: "project-1" },
    document: { id: "guide", content: "invalid source" },
  }));
  await assert.rejects(saving, error => error.status === 422);
  assert.equal(workspace.document.draftId, "saved-draft");
  assert.equal(workspace.document.draftRevision, "draft-1");
  assert.equal(workspace.document.baseRevision, "project-1");
  assert.equal(workspace.content, "fixed source typed while publishing");
  assert.equal(workspace.base, "invalid source"); assert.equal(workspace.dirty, true);
  await workspace.save(false);
  assert.equal(sent[1].draftId, "saved-draft"); assert.equal(sent[1].draftRevision, "draft-1");
  assert.equal(sent[1].content, "fixed source typed while publishing");
  assert.equal(workspace.document.draftRevision, "draft-2"); assert.equal(workspace.dirty, false);
});

test("theme palette input clears inherited overrides and rejects CSS code", () => {
  const result = mergeThemeFields({ id: "custom", revision: "r1", builtin: false, sync: { retryable: true }, light: { background: "#ffffff", text: "#172033" } }, { "light.background": "", "dark.accent": "#aabbcc" });
  assert.deepEqual(result.light, { text: "#172033" }); assert.deepEqual(result.dark, { accent: "#aabbcc" }); assert.equal(result.revision, "r1");
  assert.equal(Object.hasOwn(result, "sync"), false); assert.equal(Object.hasOwn(result, "builtin"), false);
  assert.throws(() => mergeThemeFields({}, { "light.background": "url(https://outside.test/)" }), /hexadecimal color/);
  assert.throws(() => mergeThemeFields({}, { "dark.text": "#fff;display:none" }), /hexadecimal color/);
});

test("theme selections keep token values while discarding registry-only metadata", () => {
  const original = { revision: "project-1", title: "Docs", sync: { retryable: true }, theme: { id: "default", name: "Default", revision: "old-registry", builtin: true } };
  const selected = mergeSettings(original, { theme: { id: "custom", name: "Custom", revision: "new-registry", builtin: false, accent: "#123456" } });
  assert.equal(selected.revision, "project-1"); assert.equal(selected.theme.id, "custom"); assert.equal(selected.theme.accent, "#123456");
  assert.equal(Object.hasOwn(selected.theme, "revision"), false); assert.equal(Object.hasOwn(selected.theme, "builtin"), false);
  assert.equal(Object.hasOwn(selected, "sync"), false); assert.equal(original.theme.revision, "old-registry");
});
