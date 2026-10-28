import test from "node:test";
import assert from "node:assert/strict";
import { API, APIError, Workspace, MAX_SOURCE_BYTES, formatLanguage } from "../assets/core.mjs";
import { deleteTreePlan, insideFolder, literalCodePath, textFilePath, newTextFilePath, registrationKind, sourceDraftOverlap, registrationReview, registrationRequest, sourceHistorySnapshot, UPLOAD_ACCEPT, reviewFolderDeletion } from "../assets/files.mjs";

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
  const api = { async source(path, options = {}) { reads.push({ path, options }); return options.draftId ? source("old source", "d0", "p0") : source("latest published", "p9"); }, async request(path, options) { requests.push({ path, options }); return path.startsWith("files/rebase?") ? { revision: "p9", published: false, draft: metadata("file-draft", "d2", "p9") } : saved(metadata("file-draft", "d1", "p0")); } };
  const workspace = new Workspace(api); await workspace.openFile("examples/main.go", { draftId: "file-draft", draftRevision: "d0" }); workspace.edit("\uFEFF// новый пример\r\n");
  await workspace.save(); const request = requests[0]; assert.equal(request.path, "files/content?path=examples%2Fmain.go"); assert.equal(request.options.raw, true); assert.equal(request.options.body, "\uFEFF// новый пример\r\n"); assert.deepEqual(request.options.headers, { "Content-Type": "application/octet-stream", "If-Match": '"p0"', "X-Draft-ID": "file-draft", "X-Draft-Revision": "d0" });
  assert.equal(workspace.document.resource, "file"); assert.equal(workspace.document.revision, "p0"); assert.equal(workspace.document.draftRevision, "d1"); assert.equal(workspace.dirty, false);
  const latest = await workspace.latest(); assert.equal(latest.revision, "p9"); assert.equal(workspace.document.baseRevision, "p0");
  assert.equal(requests.length, 1); assert.equal(reads.at(-1).options.draftId, undefined);
  await workspace.rebase(latest.revision); assert.equal(requests.length, 2); assert.equal(requests[1].path, "files/rebase?path=examples%2Fmain.go"); assert.deepEqual(requests[1].options.headers, { "Content-Type": "application/octet-stream", "If-Match": '"p9"', "X-Draft-ID": "file-draft", "X-Draft-Revision": "d1" }); assert.equal(requests[1].options.body, "\uFEFF// новый пример\r\n"); assert.equal(workspace.document.baseRevision, "p9"); assert.equal(workspace.document.draftRevision, "d2"); assert.equal(workspace.dirty, false);
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

test("a new empty text buffer is dirty and sends no request until its first save", async () => {
  const requests = [];
  const workspace = new Workspace({ async request(path, options) { requests.push({ path, options }); return { revision: "p1", published: false, draft: { id: "empty", revision: "d1", baseRevision: "p1", path: "empty.txt", operation: "write" } }; } });
  assert.equal(await workspace.createFile("empty.txt", { revision: "p1" }), true); assert.equal(requests.length, 0); assert.equal(workspace.document.resource, "file"); assert.equal(workspace.document.isNew, true); assert.equal(workspace.content, ""); assert.equal(workspace.dirty, true);
  await workspace.save(); assert.equal(requests.length, 1); assert.equal(requests[0].path, "files/content?path=empty.txt"); assert.equal(requests[0].options.body, ""); assert.equal(requests[0].options.headers["If-Match"], '"p1"'); assert.equal(workspace.document.isNew, false); assert.equal(workspace.document.draftId, "empty"); assert.equal(workspace.dirty, false);
});

test("creating a local buffer respects dirty cancellation and cannot override a newer selection during confirmation", async () => {
  let requests = 0, finish;
  const canceled = new Workspace({ request() { requests++; } }, { confirmDiscard: () => false });
  await canceled.createFile("first.txt", { revision: "p1" }); canceled.edit("keep me"); const previous = canceled.document;
  assert.equal(await canceled.createFile("second.txt", { revision: "p1" }), false); assert.equal(canceled.document, previous); assert.equal(canceled.content, "keep me"); assert.equal(requests, 0);
  const confirmation = new Promise(resolve => { finish = resolve; });
  const workspace = new Workspace({ async source() { return source("new selection", "p2"); } }, { confirmDiscard: () => confirmation });
  await workspace.createFile("first.txt", { revision: "p1" }); workspace.edit("older input"); const creating = workspace.createFile("second.txt", { revision: "p1" });
  await workspace.openFile("selected.sql", { force: true }); finish(true); assert.equal(await creating, false); assert.equal(workspace.document.path, "selected.sql"); assert.equal(workspace.content, "new selection");
  await assert.rejects(workspace.createFile("invalid.txt", { revision: "bad\r\nrevision" }), error => error.status === 409); await assert.rejects(workspace.createFile("readonly.txt", { revision: "p2", canWrite: false }), error => error.status === 403); assert.equal(workspace.document.path, "selected.sql");
});

test("explicit source rebase without a saved draft stages the reviewed bytes once and preserves newer input", async () => {
  let finish; const pending = new Promise(resolve => { finish = resolve; }); const requests = [];
  const workspace = new Workspace({ async source() { return source("published", "p0"); }, async request(path, options) { requests.push({ path, options }); return pending; } });
  await workspace.openFile("examples/main.go"); const merged = "\uFEFF// объединено\r\n"; workspace.edit(merged); const rebasing = workspace.rebase("p2"); workspace.edit(merged + "// newer\r\n");
  await assert.rejects(workspace.rebase("p2"), error => error.code === "busy");
  finish({ revision: "p2", published: false, draft: metadata("new-merged", "d2", "p2") }); await rebasing;
  assert.equal(requests.length, 1); assert.equal(requests[0].path, "files/rebase?path=examples%2Fmain.go"); assert.equal(requests[0].options.method, "POST"); assert.equal(requests[0].options.raw, true); assert.equal(requests[0].options.body, merged); assert.deepEqual(requests[0].options.headers, { "Content-Type": "application/octet-stream", "If-Match": '"p2"' });
  assert.equal(workspace.document.draftId, "new-merged"); assert.equal(workspace.document.baseRevision, "p2"); assert.equal(workspace.base, merged); assert.equal(workspace.content, merged + "// newer\r\n"); assert.equal(workspace.dirty, true);
});

test("failed source rebase keeps owner CAS and content without automatically selecting or retrying a draft", async () => {
  const requests = [];
  const workspace = new Workspace({ async source() { return source("old saved", "d0", "p0"); }, async request(path, options) { requests.push({ path, options }); throw new APIError("The reviewed version changed", 409, "conflict", { currentRevision: "p3" }); } });
  await workspace.openFile("examples/main.go", { draftId: "owned", draftRevision: "d0" }); workspace.edit("\uFEFFmy merge\r\n"); const previous = workspace.document;
  await assert.rejects(workspace.rebase("p2"), error => error.status === 409); await Promise.resolve(); assert.equal(requests.length, 1); assert.equal(requests[0].options.headers["X-Draft-ID"], "owned"); assert.equal(requests[0].options.headers["X-Draft-Revision"], "d0"); assert.equal(requests[0].options.headers["If-Match"], '"p2"'); assert.equal(workspace.document, previous); assert.equal(workspace.document.baseRevision, "p0"); assert.equal(workspace.content, "\uFEFFmy merge\r\n"); assert.equal(workspace.conflict.revision, "p3"); assert.equal(workspace.dirty, true);
});

test("an abandoned source rebase cannot resurrect its file or trigger publication", async () => {
  let finish; const pending = new Promise(resolve => { finish = resolve; }); const paths = [];
  const workspace = new Workspace({ async source() { return source(); }, async request(path) { paths.push(path); return pending; } });
  await workspace.openFile("examples/main.go"); workspace.edit("submitted merge"); const rebasing = workspace.rebase("p2"); workspace.clear(); finish({ revision: "p2", published: false, draft: metadata("merged", "d2", "p2") }); await rebasing;
  assert.deepEqual(paths, ["files/rebase?path=examples%2Fmain.go"]); assert.equal(workspace.document, null); assert.equal(workspace.content, "");
});

test("source rebase rejects a response for another owner draft before adopting any state", async () => {
  let calls = 0;
  const workspace = new Workspace({ async source() { return source("saved", "d0", "p0"); }, async request() { calls++; return { revision: "p2", published: false, draft: metadata("some-other-draft", "d2", "p2") }; } });
  await workspace.openFile("examples/main.go", { draftId: "owned", draftRevision: "d0" }); workspace.edit("keep merged bytes\r\n"); const previous = workspace.document;
  await assert.rejects(workspace.rebase("p2"), /incomplete/); assert.equal(calls, 1); assert.equal(workspace.document, previous); assert.equal(workspace.document.draftRevision, "d0"); assert.equal(workspace.content, "keep merged bytes\r\n"); assert.equal(workspace.dirty, true);
});

test("new text filenames reject published aliases and overlapping saved operations without inventing page kinds", () => {
  assert.equal(newTextFilePath("example.HTML", "snippets", [], []), "snippets/example.HTML"); assert.equal(formatLanguage({ resource: "file", path: "snippets/example.HTML" }), "text");
  assert.throws(() => newTextFilePath("existing.txt", ".", [{ path: "EXISTING.TXT" }]), error => error.status === 409);
  for (const draft of [{ path: "snippets", operation: "delete-tree" }, { path: "old.txt", to: "snippets/new.txt", operation: "move" }, { path: "snippets/new.txt", operation: "write" }]) assert.throws(() => newTextFilePath("new.txt", "snippets", [], [draft]), error => error.status === 409);
  for (const name of ["../escape.txt", "folder/file.txt", "folder\\file.txt", "run.exe"]) assert.throws(() => newTextFilePath(name, "."), { name: "APIError" });
});

test("source GET exposes a strong content digest while preserving BOM and CRLF and allowing ordinary draft reads", async () => {
  const content = "\uFEFF// точные байты\r\n", digest = "a".repeat(64), project = "1".repeat(64);
  const api = new API("https://docs.test/api/", async () => new Response(content, { headers: { ETag: '"' + project + '"', "X-Source-Revision": digest } }));
  const live = await api.source("source.txt"); assert.equal(live.content, content); assert.equal(live.sourceRevision, digest); assert.equal(live.revision, project); assert.equal(api.pending.size, 0);
  const draft = new API("https://docs.test/api/", async () => attachment(content, "d1", project)); const saved = await draft.source("source.txt", { draftId: "owner" }); assert.equal(saved.content, content); assert.equal(saved.sourceRevision, ""); assert.equal(saved.baseRevision, project);
  for (const digest of ["not-a-digest", "A".repeat(64), "z".repeat(64)]) { const malformed = new API("https://docs.test/api/", async () => new Response(content, { headers: { ETag: '"' + project + '"', "X-Source-Revision": digest } })); await assert.rejects(malformed.source("source.txt"), /valid content revision/); assert.equal(malformed.pending.size, 0); }
});

test("registration review requires both exact live source and project tokens and excludes registered or binary sources", () => {
  const revision = "1".repeat(64), sourceRevision = "a".repeat(64), live = { content: "\uFEFFsaved\r\n", revision, sourceRevision, baseRevision: "" }, catalog = { revision, documents: [] };
  const reviewed = registrationReview("source.md", live, catalog); assert.equal(reviewed.content, live.content); assert.equal(Object.isFrozen(reviewed), true); assert.equal(reviewed.sourceRevision, sourceRevision);
  for (const source of [{ ...live, sourceRevision: "" }, { ...live, sourceRevision: "a".repeat(63) }, { ...live, baseRevision: revision }, { ...live, content: "binary\u0000data" }]) assert.throws(() => registrationReview("source.md", source, catalog), { name: "APIError" });
  assert.throws(() => registrationReview("source.md", live, { ...catalog, revision: "2".repeat(64) }), error => error.status === 409); assert.throws(() => registrationReview("source.md", live, { ...catalog, documents: [{ path: "SOURCE.MD" }] }), error => error.status === 409); assert.throws(() => registrationReview("image.png", live, catalog), error => error.status === 415);
  assert.equal(registrationReview("empty.txt", { ...live, content: "" }, catalog).content, "");
});

test("explicit registration accepts all text suffixes and preserves live digest without sending source bytes", () => {
  const revision = "1".repeat(64), sourceRevision = "a".repeat(64);
  for (const extension of ["md", "markdown", "html", "htm", "proto", "json", "yaml", "yml", "txt", "go", "js", "ts", "sh", "sql", "css", "xml"]) {
    const path = "sources/example." + extension, reviewed = registrationReview(path, { content: "\uFEFFliteral source\r\n", revision, sourceRevision }, { revision, documents: [] });
    for (const kind of ["markdown", "html", "protobuf", "openapi"]) { const request = registrationRequest(reviewed, { title: "Explicit format", kind, id: "stable.docs_1" }); assert.equal(request.kind, kind); assert.equal(request.path, path); assert.equal(request.revision, revision); assert.equal(request.sourceRevision, sourceRevision); assert.equal("content" in request, false); assert.equal("draftId" in request, false); }
  }
  assert.equal(registrationKind("a.HTML"), "html"); assert.equal(registrationKind("a.proto"), "protobuf"); assert.equal(registrationKind("a.yaml"), "openapi"); assert.equal(registrationKind("a.go"), "markdown");
});

test("registration metadata bounds IDs, Unicode titles and portable page URLs independently of the source path cap", () => {
  const reviewed = { path: "source.md", revision: "1".repeat(64), sourceRevision: "a".repeat(64) }, fields = { title: "Пример", kind: "markdown", id: "a".repeat(128) };
  const longURL = "guide/".repeat(350) + "example.html"; assert.ok(longURL.length > 2048); assert.equal(registrationRequest(reviewed, { ...fields, outputPath: longURL }).outputPath, longURL);
  for (const changes of [{ title: "\nTitle" }, { title: "Ж".repeat(129) }, { title: " " }, { id: "Uppercase" }, { id: "a".repeat(129) }, { kind: "javascript" }, ...["_docs/index.html", "assets/x.html", ".hidden/x.html", "guide/../x.html", "con.html", "https://docs.test/x.html", "Guide/x.html", "x.html?query", "x".repeat(4097) + ".html"].map(outputPath => ({ outputPath }))]) assert.throws(() => registrationRequest(reviewed, { ...fields, ...changes }), { name: "APIError" });
  const unicode = { ...reviewed, path: "пример.md" }; assert.throws(() => registrationRequest(unicode, fields), /lowercase ASCII/); assert.equal(registrationRequest(unicode, { ...fields, outputPath: "manual/example.html" }).outputPath, "manual/example.html"); assert.equal("id" in registrationRequest(reviewed, { title: "Generated ID", kind: "markdown", id: "" }), false);
});

test("visible overlapping drafts are advisory when their active branch cannot be identified", () => {
  const path = "docs/source.md", revision = "1".repeat(64), drafts = [{ path, branch: "other", operation: "write" }];
  assert.equal(sourceDraftOverlap(path, drafts), true); assert.equal(sourceDraftOverlap(path, [{ path: "docs2/source.md" }]), false); assert.equal(sourceDraftOverlap(path, [{ changes: [{ path: "old", to: "docs" }] }]), true);
  const review = registrationReview(path, { content: "published", revision, sourceRevision: "a".repeat(64) }, { revision, documents: [] }, drafts); assert.equal(review.pendingChanges, true); assert.equal(registrationRequest(review, { title: "Explicit active branch check", kind: "markdown" }).path, path);
});

test("history snapshots permit zero-byte versions while rejecting other paths and unsafe restore metadata", () => {
  const item = { id: "h1", path: "deleted.md", revision: "p0", author: "Author <script>", createdAt: "2026-10-28T00:00:00Z", size: 0, deleted: false, restorable: true, documentId: "historical-id", outputPath: "private.html" };
  const snapshot = { path: item.path, revision: "p2", revisions: [item, { ...item, id: "deleted", deleted: true, restorable: false }] };
  const result = sourceHistorySnapshot(snapshot, item.path); assert.equal(result.revisions[0].restorable, true); assert.equal(result.revisions[0].size, 0); assert.equal(result.revisions[1].restorable, false); assert.equal(result.revisions[0].documentId, undefined); assert.equal(result.revisions[0].outputPath, undefined);
  for (const invalid of [{ ...snapshot, path: "private.md" }, { ...snapshot, revisions: [{ ...item, path: "private.md" }] }, { ...snapshot, revisions: [item, item] }, { ...snapshot, revisions: [{ ...item, deleted: true }] }, { ...snapshot, revisions: [{ ...item, size: -1 }] }, { ...snapshot, revisions: new Array(257).fill(item) }]) assert.throws(() => sourceHistorySnapshot(invalid, item.path), { name: "APIError" });
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
