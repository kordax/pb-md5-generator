import test from "node:test";
import assert from "node:assert/strict";
import { APIError, Workspace, documentProperties, validateDocumentProperties, formatLanguage } from "../assets/core.mjs";

const initial = () => ({ id: "guide", path: "docs/guide.md", title: "Stored title", kind: "markdown", outputPath: "guide.html", content: "\uFEFF# Original\r\n", revision: "old-base", publishedRevision: "current-project", draft: true, draftId: "owner-draft", draftRevision: "d0", baseRevision: "old-base", canWrite: true });
const response = (doc, body, revision = "d1") => ({ revision: body.revision, published: false, draft: { id: doc.draftId || "new-draft", revision, baseRevision: body.revision, path: doc.path, operation: "write" }, document: { ...doc, ...body.properties, content: body.content, revision: body.revision, publishedRevision: body.propertiesRevision || doc.publishedRevision } });

test("property-only saves carry the current metadata CAS separately from the stale source base and content-only resaves omit properties", async () => {
  let stored = initial(); const requests = [];
  const workspace = new Workspace({ async request(path, options) { if (!options?.method) return stored; requests.push({ path, ...options }); const result = response(stored, options.body, "d" + requests.length); stored = { ...result.document, draftId: result.draft.id, draftRevision: result.draft.revision, baseRevision: result.draft.baseRevision }; return result; } });
  await workspace.open("guide"); workspace.editProperties({ title: "Pending title" }); assert.equal(workspace.dirty, true); assert.equal(workspace.content, "\uFEFF# Original\r\n"); await workspace.save();
  assert.equal(requests[0].path, "documents/guide"); assert.equal(requests[0].body.revision, "old-base"); assert.equal(requests[0].body.baseRevision, "old-base"); assert.equal(requests[0].body.propertiesRevision, "current-project"); assert.deepEqual(requests[0].body.properties, { title: "Pending title", kind: "markdown", outputPath: "guide.html" }); assert.equal(requests[0].body.draftId, "owner-draft"); assert.equal(requests[0].body.draftRevision, "d0"); assert.equal(requests[0].body.content, "\uFEFF# Original\r\n"); assert.equal(workspace.dirty, false);
  workspace.edit("\uFEFF# Changed text\r\n"); await workspace.save(); assert.equal("properties" in requests[1].body, false); assert.equal("propertiesRevision" in requests[1].body, false); assert.equal(requests[1].body.draftRevision, "d1"); assert.equal(workspace.properties.title, "Pending title"); assert.equal(workspace.document.id, "guide"); assert.equal(workspace.document.path, "docs/guide.md");
});

test("saving captures source and properties atomically and preserves both kinds of newer local edits", async () => {
  let finish, submitted; const pending = new Promise(resolve => { finish = resolve; }); const doc = initial();
  const workspace = new Workspace({ async request(_path, options) { if (!options?.method) return doc; submitted = options.body; return pending; } });
  await workspace.open("guide"); workspace.edit("\uFEFFsubmitted\r\n"); workspace.editProperties({ title: "Submitted title", kind: "html" }); const saving = workspace.save(); workspace.edit("\uFEFFnewer text\r\n"); workspace.editProperties({ title: "Newer title", outputPath: "newer.html" }); finish(response(doc, submitted)); await saving;
  assert.equal(submitted.content, "\uFEFFsubmitted\r\n"); assert.equal(submitted.properties.title, "Submitted title"); assert.equal(workspace.base, "\uFEFFsubmitted\r\n"); assert.equal(workspace.baseProperties.title, "Submitted title"); assert.equal(workspace.content, "\uFEFFnewer text\r\n"); assert.equal(workspace.properties.title, "Newer title"); assert.equal(workspace.properties.outputPath, "newer.html"); assert.equal(workspace.dirty, true);
});

test("explicit rebase sends the full selected properties even when unchanged and never discovers another draft", async () => {
  const doc = { ...initial(), draft: false, draftId: "", draftRevision: "", baseRevision: "" }, requests = [];
  const workspace = new Workspace({ async request(path, options) { requests.push({ path, options }); return options?.method ? response(doc, options.body) : doc; } });
  await workspace.open("guide"); workspace.edit("merged source\r\n"); assert.equal(workspace.propertiesDirty, false); await workspace.rebase("reviewed-project", { propertiesRevision: "reviewed-project" });
  assert.equal(requests.length, 2); assert.equal(requests[1].path, "documents/guide/rebase"); assert.deepEqual(requests[1].options.body.properties, documentProperties(doc)); assert.equal(requests[1].options.body.propertiesRevision, "reviewed-project"); assert.equal("draftId" in requests[1].options.body, false); assert.equal(workspace.document.id, doc.id); assert.equal(workspace.document.path, doc.path); assert.equal(workspace.document.baseRevision, "reviewed-project"); assert.equal(workspace.dirty, false);
});

test("unpublished comparison checks the current project without resetting local source or properties", async () => {
  const doc = initial(), requests = [];
  const workspace = new Workspace({ async request(path, options) { requests.push({ path, options }); if (path.endsWith("?published=true") && path.startsWith("documents/")) throw new APIError("Not published", 404); if (path === "documents?published=true") return { revision: "reviewed-project", documents: [] }; return options?.method ? response(doc, options.body) : doc; } });
  await workspace.open("guide"); workspace.edit("\uFEFFunsaved source\r\n"); workspace.editProperties({ title: "Unpublished title" }); const before = workspace.document; const latest = await workspace.latest();
  assert.equal(latest.unpublished, true); assert.equal(latest.publishedRevision, "reviewed-project"); assert.equal(workspace.document, before); assert.equal(workspace.content, "\uFEFFunsaved source\r\n"); assert.equal(workspace.properties.title, "Unpublished title"); await workspace.rebase(latest.publishedRevision, { propertiesRevision: latest.publishedRevision }); assert.equal(requests.at(-1).options.body.properties.title, "Unpublished title"); assert.equal(workspace.document.baseRevision, "reviewed-project");
});

test("late failed saves and mismatched identities cannot replace another editor or metadata state", async () => {
  let reject; const pending = new Promise((_resolve, failure) => { reject = failure; }); const doc = initial();
  const workspace = new Workspace({ async request(_path, options) { return options?.method ? pending : doc; } }); await workspace.open("guide"); workspace.editProperties({ title: "Retain locally" }); const saving = workspace.save(); workspace.clear(); reject(new APIError("Changed", 409, "conflict")); await assert.rejects(saving, error => error.status === 409); assert.equal(workspace.document, null); assert.equal(workspace.properties, null); assert.equal(workspace.conflict, null);
  workspace.api.request = async (_path, options) => options?.method ? { ...response(doc, options.body), document: { ...doc, id: "different-id" } } : doc; await workspace.open("guide"); workspace.editProperties({ title: "Keep identity" }); const before = workspace.document; await assert.rejects(workspace.save(), /changed the document identity/); assert.equal(workspace.document, before); assert.equal(workspace.properties.title, "Keep identity"); assert.equal(workspace.dirty, true);
});

test("properties validate portable metadata, protect identity and keep explicit format separate from source suffix", async () => {
  const valid = { title: "Ж".repeat(128), kind: "markdown", outputPath: "manual/guide.html" }; assert.deepEqual(validateDocumentProperties(valid), valid);
  for (const update of [{ title: "\nTitle" }, { title: "Ж".repeat(129) }, { kind: "javascript" }, { outputPath: "" }, { outputPath: "../escape.html" }, { outputPath: "assets/x.html" }, { outputPath: "con.html" }, { id: "changed" }]) assert.throws(() => validateDocumentProperties({ ...valid, ...update }), { name: "APIError" });
  const workspace = new Workspace({ async request() { return initial(); } }); await workspace.open("guide"); assert.throws(() => workspace.editProperties({ id: "changed" }), /permanent/); assert.throws(() => workspace.editProperties({ path: "elsewhere.md" }), /permanent/); assert.equal(workspace.document.id, "guide"); assert.equal(workspace.document.path, "docs/guide.md");
  assert.equal(formatLanguage({ kind: "markdown", path: "source.proto" }), "markdown"); assert.equal(formatLanguage({ kind: "markdown", path: "source.yaml" }), "markdown"); assert.equal(formatLanguage({ kind: "openapi", path: "source.yaml" }), "yaml"); assert.equal(formatLanguage({ resource: "file", kind: "html", path: "source.html" }), "text");
});
