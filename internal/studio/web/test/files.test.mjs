import test from "node:test";
import assert from "node:assert/strict";
import { API, APIError, Workspace, MAX_SOURCE_BYTES, formatLanguage } from "../assets/core.mjs";
import { deleteTreePlan, insideFolder, literalCodePath, textFilePath, UPLOAD_ACCEPT, reviewFolderDeletion } from "../assets/files.mjs";

const metadata = (id = "file-draft", revision = "d1", baseRevision = "p0") => ({ id, revision, baseRevision, path: "examples/main.go", operation: "write" });
const saved = (draft = metadata()) => ({ revision: "current-project", published: false, draft });
const source = (content = "source", revision = "p0", baseRevision = "") => ({ content, revision, baseRevision });
const attachment = (content, revision = "p0", base = "") => new Response(content, { headers: { "Content-Type": "application/octet-stream", ETag: '"' + revision + '"', ...(base ? { "X-Base-Revision": base } : {}) } });

test("bounded source GET preserves UTF-8 BOM, Unicode and CRLF with independent saved-draft revisions", async () => {
  const bytes = new TextEncoder().encode("\uFEFF// пример\r\n<script>neverExecute()</script>\r\n"); const requests = [];
  const api = new API("https://docs.test/team/_docs/api/v1/", async (url, options) => { requests.push({ url, options }); return attachment(bytes, url.searchParams.has("draftId") ? "draft-cas" : "project-cas", url.searchParams.has("draftId") ? "old-base" : ""); });
  const published = await api.source("examples/main.go"); assert.deepEqual(new TextEncoder().encode(published.content), bytes); assert.equal(published.revision, "project-cas");
  const draft = await api.source("examples/main.go", { draftId: "owned-draft" }); assert.equal(draft.revision, "draft-cas"); assert.equal(draft.baseRevision, "old-base");
  assert.equal(requests[0].options.headers.Accept, "application/octet-stream"); assert.equal(requests[0].options.credentials, "same-origin"); assert.equal(requests[0].options.redirect, "error"); assert.equal(requests[1].url.searchParams.get("draftId"), "owned-draft"); assert.equal(api.pending.size, 0);
});

test("source decoding rejects binary, invalid UTF-8, missing revision and incomplete draft metadata", async () => {
  for (const response of [attachment(new Uint8Array([0xff])), attachment(new Uint8Array([97, 0, 98])), new Response("text"), new Response("text", { headers: { ETag: 'W/"weak"' } })]) {
    const api = new API("https://docs.test/api/", async () => response);
    await assert.rejects(api.source("examples/main.js"), { name: "APIError" }); assert.equal(api.pending.size, 0);
  }
  const incomplete = new API("https://docs.test/api/", async () => attachment("text", "draft-cas")); await assert.rejects(incomplete.source("examples/main.js", { draftId: "d1" }), /base revision/);
  let expired = 0;
  const denied = new API("https://docs.test/api/", async () => new Response(JSON.stringify({ error: { code: "unauthorized", message: "Sign in again" } }), { status: 401 }), () => { expired++; });
  await assert.rejects(denied.source("examples/private.go"), error => error.status === 401 && error.code === "unauthorized"); assert.equal(expired, 1);
});

test("source streaming cancels an over-limit response before retaining its bytes", async () => {
  let canceled = false;
  const stream = new ReadableStream({ start(controller) { controller.enqueue(new Uint8Array(MAX_SOURCE_BYTES + 1)); }, cancel() { canceled = true; } });
  const api = new API("https://docs.test/api/", async () => new Response(stream, { headers: { ETag: '"p1"' } }));
  await assert.rejects(api.source("examples/large.ts"), error => error.status === 413); assert.equal(canceled, true); assert.equal(api.pending.size, 0);
  let headerCanceled = false;
  const declared = new ReadableStream({ cancel() { headerCanceled = true; } });
  const declaredAPI = new API("https://docs.test/api/", async () => new Response(declared, { headers: { ETag: '"p1"', "Content-Length": String(MAX_SOURCE_BYTES + 1) } }));
  await assert.rejects(declaredAPI.source("examples/large.ts"), error => error.status === 413); assert.equal(headerCanceled, true);
});

test("source workspace resaves with owner draft CAS and original stale base, without silent rebase", async () => {
  const requests = [], reads = [];
  const api = { async source(path, options = {}) { reads.push({ path, options }); return options.draftId ? source("old source", "d0", "p0") : source("latest published", "p9"); }, async request(path, options) { requests.push({ path, options }); return saved(metadata("file-draft", "d1", "p0")); } };
  const workspace = new Workspace(api); await workspace.openFile("examples/main.go", { draftId: "file-draft", draftRevision: "d0" }); workspace.edit("\uFEFF// новый пример\r\n");
  await workspace.save(); const request = requests[0]; assert.equal(request.path, "files/content?path=examples%2Fmain.go"); assert.equal(request.options.raw, true); assert.equal(request.options.body, "\uFEFF// новый пример\r\n"); assert.deepEqual(request.options.headers, { "Content-Type": "application/octet-stream", "If-Match": '"p0"', "X-Draft-ID": "file-draft", "X-Draft-Revision": "d0" });
  assert.equal(workspace.document.resource, "file"); assert.equal(workspace.document.revision, "p0"); assert.equal(workspace.document.draftRevision, "d1"); assert.equal(workspace.dirty, false);
  const latest = await workspace.latest(); assert.equal(latest.revision, "p9"); assert.equal(workspace.document.baseRevision, "p0");
  await assert.rejects(workspace.rebase("p9"), /original base/); assert.equal(requests.length, 1); assert.equal(reads.at(-1).options.draftId, undefined);
});

test("source opens protect dirty documents and discard late or stale-draft responses", async () => {
  let fileReads = 0;
  const denied = new Workspace({ async source() { fileReads++; return source(); } }, { confirmDiscard: () => false }); denied.document = { id: "guide", path: "guide.md" }; denied.base = "old"; denied.content = "unsaved";
  assert.equal(await denied.openFile("examples/main.go"), false); assert.equal(fileReads, 0); assert.equal(denied.content, "unsaved");
  let resolveFile; const pending = new Promise(resolve => { resolveFile = resolve; });
  const workspace = new Workspace({ async source() { return pending; }, async request() { return { id: "guide", revision: "p1", content: "newer document" }; } });
  const opening = workspace.openFile("examples/slow.go"); await Promise.resolve(); await workspace.open("guide"); resolveFile(source("late source")); assert.equal(await opening, false); assert.equal(workspace.document.id, "guide");
  const previous = workspace.document; workspace.api.source = async () => source("new draft", "d2", "p1");
  await assert.rejects(workspace.openFile("examples/main.go", { draftId: "file-draft", draftRevision: "d1" }), error => error.status === 409); assert.equal(workspace.document, previous);
});

test("source saving preserves newer edits and gates duplicate saves", async () => {
  let finish; const pending = new Promise(resolve => { finish = resolve; }); let writes = 0;
  const workspace = new Workspace({ async source() { return source("original"); }, async request() { writes++; return pending; } });
  await workspace.openFile("examples/main.go"); workspace.edit("submitted"); const saving = workspace.save(); workspace.edit("newer edits");
  await assert.rejects(workspace.save(), error => error.code === "busy"); finish(saved()); await saving;
  assert.equal(writes, 1); assert.equal(workspace.content, "newer edits"); assert.equal(workspace.base, "submitted"); assert.equal(workspace.dirty, true);
  assert.throws(() => workspace.edit("binary\u0000content"), error => error.code === "not_text"); assert.equal(workspace.content, "newer edits");
});

test("source publication failures retain the staged draft, and successful publication clears old CAS", async () => {
  let fail = true; const requests = [];
  const workspace = new Workspace({ async source() { return source("old"); }, async request(path, options) { requests.push({ path, options }); if (options.method === "PUT") return saved(); if (fail) throw new APIError("Publication changed", 409, "conflict", { currentRevision: "p9" }); return { revision: "p10", published: true }; } });
  await workspace.openFile("examples/main.go"); workspace.edit("new source"); await assert.rejects(workspace.save(true, "Publish source"), error => error.status === 409);
  assert.equal(workspace.document.draftId, "file-draft"); assert.equal(workspace.document.draftRevision, "d1"); assert.equal(workspace.document.baseRevision, "p0"); assert.equal(workspace.content, "new source"); assert.ok(workspace.conflict);
  assert.deepEqual(requests[1].options.body, { revision: "d1", message: "Publish source", push: false });
  fail = false; await workspace.save(true); assert.equal(workspace.document.revision, "p10"); assert.equal(workspace.document.draftId, ""); assert.equal(workspace.document.draftRevision, ""); assert.equal(workspace.conflict, null);
});

test("automatic source publication failures expose retained draft CAS without dropping newer edits", async () => {
  let rejectSave; const request = new Promise((_resolve, reject) => { rejectSave = reject; });
  const workspace = new Workspace({ async source() { return source("old"); }, async request() { return request; } });
  await workspace.openFile("examples/main.go"); workspace.edit("staged source"); const saving = workspace.save(); workspace.edit("newer local source");
  rejectSave(new APIError("Publication failed", 409, "conflict", { currentRevision: "p2", draft: metadata("kept", "d2", "p0") }));
  await assert.rejects(saving, error => error.status === 409); assert.equal(workspace.document.draftId, "kept"); assert.equal(workspace.document.draftRevision, "d2"); assert.equal(workspace.document.baseRevision, "p0"); assert.equal(workspace.base, "staged source"); assert.equal(workspace.content, "newer local source"); assert.equal(workspace.dirty, true);
  let writes = 0; const published = new Workspace({ async source() { return source(); }, async request() { writes++; return { revision: "p3", published: true }; } });
  await published.openFile("examples/main.go"); published.edit("published"); await published.save(true); assert.equal(writes, 1); assert.equal(published.document.revision, "p3"); assert.equal(published.document.draftId, "");
});

test("abandoning a source workspace cannot resurrect it or trigger a follow-up publish", async () => {
  let finish; const pending = new Promise(resolve => { finish = resolve; }); const paths = [];
  const workspace = new Workspace({ async source() { return source(); }, async request(path) { paths.push(path); return pending; } }); await workspace.openFile("examples/main.go"); workspace.edit("submitted"); const saving = workspace.save(true); workspace.clear(); finish(saved()); await saving;
  assert.equal(paths.length, 1); assert.equal(workspace.document, null); assert.equal(workspace.content, "");
});

function plan(count = 2) { return { path: "examples", revision: "p1", planRevision: "plan-cas", fileCount: count - 1, directoryCount: 1, documentCount: 0, totalBytes: count - 1, entries: Array.from({ length: Math.min(count, 100) }, (_, index) => index === 0 ? { path: "examples", directory: true, size: 0 } : { path: "examples/file" + index + ".go", directory: false, size: 1 }), truncated: count > 100 }; }

test("folder deletion validates complete bounded counts while truncation limits only the preview", () => {
  assert.equal(deleteTreePlan(plan(), "examples", "p1").fileCount, 1); assert.equal(deleteTreePlan(plan(128), "examples", "p1").entries.length, 100);
  for (const value of [{ ...plan(), path: "other" }, { ...plan(), revision: "p2" }, { ...plan(), entries: [] }, { ...plan(), truncated: true }, { ...plan(), fileCount: 128 }, { ...plan(), directoryCount: 1024 }, { ...plan(), totalBytes: (64 << 20) + 1 }, { ...plan(), documentCount: 3 }, { ...plan(), entries: [{ path: "examples", directory: true, size: 0 }, { path: "examples2/private.go", directory: false, size: 1 }] }, { ...plan(), entries: [null, null] }]) assert.throws(() => deleteTreePlan(value, "examples", "p1"), { name: "APIError" });
  assert.equal(insideFolder("examples/main.go", "examples"), true); assert.equal(insideFolder("examples2/main.go", "examples"), false);
});

test("partial folder grants and viewer ceilings reject review before any request", async () => {
  let calls = 0;
  for (const user of [{ role: "viewer" }, { role: "editor", grants: { read: ["examples/public"], write: ["."] } }, { role: "admin", grants: { read: ["."], write: ["examples/public"] } }]) await assert.rejects(reviewFolderDeletion({ user, folder: "examples", revision: "p1", api: { request() { calls++; } } }), error => error.status === 403);
  assert.equal(calls, 0);
});

test("literal examples select the explicit text editor and keep their upload suffixes", () => {
  for (const extension of ["go", "js", "ts", "sh", "sql", "css", "xml"]) {
    const path = "examples/source." + extension; assert.equal(literalCodePath(path), true); assert.equal(textFilePath(path), true); assert.ok(UPLOAD_ACCEPT.split(",").includes("." + extension)); assert.equal(formatLanguage({ resource: "file", path }), "text");
  }
  assert.equal(literalCodePath("examples/module.jsx"), false); assert.equal(textFilePath("sample.pdf"), false); assert.equal(formatLanguage({ kind: "markdown", path: "guide.md" }), "markdown");
});
