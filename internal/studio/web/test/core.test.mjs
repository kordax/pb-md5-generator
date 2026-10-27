import test from "node:test";
import assert from "node:assert/strict";
import { API, APIError, Workspace, MAX_SOURCE_BYTES, can, cleanPath, mergeSettings, formatLanguage, previewDocument, textWithinLimit } from "../assets/core.mjs";

const document = (id = "guide", extra = {}) => ({ id, path: "docs/" + id + ".md", title: id, kind: "markdown", revision: "project-1", content: "# " + id + "\r\n", ...extra });
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };
const response = (body, status = 200) => new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

 test("API keeps cookies same-origin and applies in-memory CSRF only to authenticated mutations", async () => {
  const requests = [];
  const api = new API("https://docs.test/team/_docs/api/v1/", async (url, options) => { requests.push({ url: url.href, options }); return response({ user: null }); });
  await api.request("session");
  await api.request("session", { method: "POST", body: { username: "editor", password: "a secret password" } });
  await assert.rejects(api.request("documents/guide", { method: "PUT", body: {} }), error => error.status === 401);
  await assert.rejects(api.request("session", { method: "DELETE" }), error => error.status === 401);
  api.session({ csrfToken: "opaque-token" });
  await api.request("documents/guide", { method: "PUT", body: { content: "hello", revision: "r1" } });
  assert.equal(requests[0].url, "https://docs.test/team/_docs/api/v1/session");
  for (const item of requests) { assert.equal(item.options.credentials, "same-origin"); assert.equal(item.options.redirect, "error"); }
  assert.equal(requests[0].options.headers["X-CSRF-Token"], undefined);
  assert.equal(requests.at(-1).options.headers["X-CSRF-Token"], "opaque-token");
  assert.deepEqual(JSON.parse(requests.at(-1).options.body), { content: "hello", revision: "r1" });
  assert.equal(requests.at(-1).url.includes("opaque-token"), false);
  await assert.rejects(api.request("https://attacker.test/"), /Invalid workspace request/);
  await assert.rejects(api.request("../../outside"), /Invalid workspace request/);
  api.session(null);
  assert.equal(api.csrfToken, "");
});

test("API preserves typed conflict details and releases readers after bounded or malformed responses", async () => {
  const api = new API("https://docs.test/api/", async () => response({ error: { code: "conflict", message: "Changed elsewhere", currentRevision: "r2" } }, 409));
  api.session({ csrfToken: "token" });
  await assert.rejects(api.request("documents/a", { method: "PUT", body: {} }), error => error instanceof APIError && error.status === 409 && error.code === "conflict" && error.details.currentRevision === "r2");
  let canceled = false;
  api.fetcher = async () => new Response(new ReadableStream({ pull(controller) { controller.enqueue(new Uint8Array(1 << 20)); }, cancel() { canceled = true; } }));
  await assert.rejects(api.request("documents/a"), /response is too large/);
  assert.equal(canceled, true);
  api.fetcher = async () => new Response("<html>upstream error</html>", { status: 502 });
  await assert.rejects(api.request("documents/a"), /unreadable response/);
});

test("API responds to external cancellation without converting it to a server failure", async () => {
  const controller = new AbortController(); let aborted = false;
  const api = new API("https://docs.test/api/", (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener("abort", () => { aborted = true; reject(new DOMException("Canceled", "AbortError")); }, { once: true });
  }));
  const pending = api.request("documents/a", { signal: controller.signal }); controller.abort();
  await assert.rejects(pending, error => error.name === "AbortError"); assert.equal(aborted, true);
});

test("workspace preserves exact source bytes and asks before discarding a dirty document", async () => {
  let confirm = false; let calls = 0;
  const workspace = new Workspace({ request: async path => { calls++; return document(path.endsWith("second") ? "second" : "first"); } }, { confirmDiscard: () => confirm });
  await workspace.open("first");
  assert.equal(workspace.content, "# first\r\n"); assert.equal(workspace.dirty, false);
  workspace.edit("local unsaved source");
  assert.equal(await workspace.open("second"), false); assert.equal(calls, 1); assert.equal(workspace.content, "local unsaved source");
  confirm = true; assert.equal(await workspace.open("second"), true); assert.equal(workspace.document.id, "second"); assert.equal(workspace.dirty, false);
});

test("a late document response cannot replace a newer selection even if transport ignores abort", async () => {
  const first = deferred(), second = deferred(); let calls = 0;
  const workspace = new Workspace({ request: async () => (++calls === 1 ? first : second).promise });
  const openFirst = workspace.open("first"); await Promise.resolve(); const openSecond = workspace.open("second"); await Promise.resolve();
  second.resolve(document("second")); await openSecond; first.resolve(document("first")); await openFirst;
  assert.equal(workspace.document.id, "second"); assert.equal(workspace.loading, false);
});

test("saving preserves edits made while a draft request is in flight and blocks duplicate mutations", async () => {
  const saving = deferred(); const sent = [];
  const workspace = new Workspace({ request: async (path, options) => { if (options?.method !== "PUT") return document(); sent.push({ path, options }); return saving.promise; } });
  await workspace.open("guide"); workspace.edit("first edit"); const save = workspace.save(false, "A note"); workspace.edit("second edit while saving");
  await assert.rejects(workspace.save(), error => error.code === "busy");
  await assert.rejects(workspace.open("another"), error => error.code === "busy");
  saving.resolve({ revision: "project-1", published: false, draft: { id: "draft-1", revision: "draft-2", baseRevision: "project-1" } }); await save;
  assert.equal(sent.length, 1); assert.equal(sent[0].options.body.content, "first edit");
  assert.equal(workspace.content, "second edit while saving"); assert.equal(workspace.base, "first edit"); assert.equal(workspace.dirty, true);
  assert.equal(workspace.document.draft, true); assert.equal(workspace.document.draftId, "draft-1"); assert.equal(workspace.document.draftRevision, "draft-2");
});

test("conflicts and failed publications retain local content and distinct project/draft revisions", async () => {
  const requests = [];
  const workspace = new Workspace({ request: async (path, options) => {
    if (options?.method !== "PUT") return document("guide", { draft: true, draftId: "draft-a", draftRevision: "draft-r1", baseRevision: "base-r1" });
    requests.push(options.body); throw new APIError("A newer revision exists", 409, "conflict", { currentRevision: "project-2" });
  } });
  await workspace.open("guide"); workspace.edit("my preserved draft");
  await assert.rejects(workspace.save(true), /newer revision/);
  assert.equal(workspace.content, "my preserved draft"); assert.equal(workspace.dirty, true); assert.equal(workspace.busy, false);
  assert.equal(workspace.conflict.revision, "project-2");
  assert.deepEqual(requests[0], { content: "my preserved draft", revision: "project-1", publish: true, message: "", draftId: "draft-a", draftRevision: "draft-r1", baseRevision: "base-r1" });
});

test("clearing a workspace prevents an older successful save from resurrecting its document", async () => {
  const saving = deferred(); const workspace = new Workspace({ request: async (_path, options) => options?.method === "PUT" ? saving.promise : document() });
  await workspace.open("guide"); workspace.edit("private draft"); const save = workspace.save(); workspace.clear(); saving.resolve({ revision: "p2", published: false }); await save;
  assert.equal(workspace.document, null); assert.equal(workspace.content, "");
});

test("capabilities respect role ceilings, disabled accounts and segment-scoped independent allowlists", () => {
  assert.equal(can({ role: "viewer" }, "read", "docs/index.md"), true);
  assert.equal(can({ role: "viewer", grants: { write: ["docs"] } }, "write", "docs/a.md"), false);
  assert.equal(can({ role: "editor" }, "admin"), false);
  assert.equal(can({ role: "admin", disabled: true }, "admin"), false);
  const restricted = { role: "admin", grants: { read: ["docs"], write: ["docs/guides"] } };
  assert.equal(can(restricted, "read", "docs/a.md"), true); assert.equal(can(restricted, "read", "docs-old/a.md"), false);
  assert.equal(can(restricted, "write", "docs/a.md"), false); assert.equal(can(restricted, "write", "docs/guides/a.md"), true);
  assert.equal(can({ role: "editor", grants: { read: [], write: [] } }, "read", "docs/a.md"), false);
  assert.equal(can({ role: "editor" }, "unexpected"), false);
  assert.equal(can({ role: "editor", grants: { read: ["."], write: ["docs"] } }, "read", "other/file.md"), true);
  assert.equal(can({ role: "editor" }, "read", "../private"), false);
});

test("settings appearance updates preserve broader settings and source format detection", () => {
  const original = { revision: "settings-1", title: "Documentation", theme: { id: "default", name: "Default", font: "system", accent: "#315be8" }, navigation: { showTOC: false }, publishing: { gitPushOnPublish: true } };
  const updated = mergeSettings(original, { theme: { accent: "#123456" } });
  assert.equal(updated.theme.font, "system"); assert.equal(updated.theme.accent, "#123456"); assert.equal(updated.revision, "settings-1"); assert.equal(updated.publishing.gitPushOnPublish, true); assert.equal(original.theme.accent, "#315be8");
  assert.equal(formatLanguage({ path: "api.yaml", kind: "openapi" }), "yaml"); assert.equal(formatLanguage({ path: "api.proto", kind: "protobuf" }), "protobuf"); assert.equal(formatLanguage({ path: "guide.html", kind: "html" }), "html");
});

test("source/path limits and sandbox preview wrappers fail without evaluating author code", () => {
  assert.equal(textWithinLimit("λ"), "λ"); assert.throws(() => textWithinLimit("😀".repeat(MAX_SOURCE_BYTES / 4 + 1)), /too large/);
  for (const path of ["/etc/passwd", "../secret", "a/../b", "a//b", "a\\b", "a\nfile"]) assert.throws(() => cleanPath(path), /relative path/);
  const html = previewDocument('<script>window.top.alert(1)</script><img src="https://outside.test/">', "test-nonce");
  assert.match(html, /default-src 'none'/); assert.match(html, /form-action 'none'/); assert.match(html, /nonce-test-nonce/);
  const hostileNonce = previewDocument("safe", '\" onload="attack()'); assert.equal(hostileNonce.includes('onload="attack'), false);
});

test("bounded API concurrency aborts outstanding work and pre-aborted reads never call transport", async () => {
  let calls = 0;
  const api = new API("https://docs.test/api/", (_url, options) => new Promise((_resolve, reject) => { calls++; options.signal.addEventListener("abort", () => reject(new DOMException("Canceled", "AbortError")), { once: true }); }));
  const canceled = new AbortController(); canceled.abort(); await assert.rejects(api.request("files", { signal: canceled.signal }), error => error.name === "AbortError"); assert.equal(calls, 0);
  const requests = Array.from({ length: 8 }, () => api.request("files"));
  await assert.rejects(api.request("files"), error => error.status === 429); assert.equal(calls, 8);
  api.abortAll(); await Promise.allSettled(requests); assert.equal(api.pending.size, 0);
});

test("successful publication clears obsolete draft CAS while preserving newer local edits", async () => {
  const workspace = new Workspace({ request: async (_path, options) => options?.method === "PUT" ? { revision: "p2", published: true, document: { ...document(), draft: false, draftId: "obsolete", draftRevision: "obsolete-r" } } : document("guide", { draftId: "d1", draftRevision: "dr1", baseRevision: "p1" }) });
  await workspace.open("guide"); await workspace.save(true);
  assert.equal(workspace.document.revision, "p2"); assert.equal(workspace.document.draftId, ""); assert.equal(workspace.document.draftRevision, ""); assert.equal(workspace.document.baseRevision, ""); assert.equal(workspace.document.draft, false);
});

test("explicit rebase uses the reviewed project and actor draft revisions without publishing or dropping text", async () => {
  const requests = []; const saving = deferred();
  const workspace = new Workspace({ request: async (path, options) => {
    if (path.endsWith("/rebase")) { requests.push(options.body); return saving.promise; }
    return document("guide", { draftId: "draft-a", draftRevision: "draft-r1", baseRevision: "project-1" });
  } });
  await workspace.open("guide"); workspace.edit("merged text"); workspace.conflict = { revision: "project-2" };
  const pending = workspace.rebase("project-2"); workspace.edit("new changes during save"); saving.resolve({ revision: "project-2", published: false, draft: { id: "draft-a", revision: "draft-r2", baseRevision: "project-2" } }); await pending;
  assert.deepEqual(requests[0], { content: "merged text", revision: "project-2", draftId: "draft-a", draftRevision: "draft-r1" }); assert.equal(workspace.content, "new changes during save"); assert.equal(workspace.base, "merged text"); assert.equal(workspace.dirty, true); assert.equal(workspace.document.baseRevision, "project-2"); assert.equal(workspace.document.draftRevision, "draft-r2"); assert.equal(workspace.conflict, null);
});
