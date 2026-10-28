import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { parseHTML } from "linkedom";
import { boot } from "../assets/app.mjs";

// This DOM model executes real application handlers. It does not execute a
// browser, enforce CSP, or establish visual/keyboard/screen-reader acceptance.
const html = readFileSync(new URL("../assets/index.html", import.meta.url), "utf8");
const admin = { id: "u1", username: "admin", displayName: "Admin", role: "admin", disabled: false };
const source = { id: "guide", path: "docs/guide.md", title: "Guide <img src=x onerror=alert(1)>", kind: "markdown", content: "# Guide\r\n", revision: "p1", canWrite: true };
const json = (value, status = 200) => new Response(status === 204 ? null : JSON.stringify(value), { status, headers: { "content-type": "application/json" } });
const historical = (path, changes = {}) => ({ id: "h1", path, revision: "p0", author: "Author", createdAt: "2026-10-28T00:00:00Z", size: 0, deleted: false, restorable: true, ...changes });
const settle = async () => { for (let i = 0; i < 12; i++) await new Promise(resolve => setImmediate(resolve)); };
const controls = (doc, label) => [...doc.querySelectorAll("button")].filter(node => node.textContent === label);
const deletionPlan = (count = 3) => ({ path: "docs", revision: "p1", planRevision: "plan-1", fileCount: count - 1, directoryCount: 1, documentCount: 1, totalBytes: count - 1, entries: Array.from({ length: Math.min(count, 100) }, (_, index) => index === 0 ? { path: "docs", directory: true, size: 0 } : { path: index === 1 ? "docs/guide.md" : "docs/file" + index + ".go", directory: false, size: 1 }), truncated: count > 100 });
const storageSnapshot = (backend = "git", revision = "c1", projectRevision = "p1") => ({ settings: { revision, backend, pullRemote: backend === "git" ? "origin" : "", remotes: backend === "git" ? [{ name: "origin", url: "git@example.com:team/docs.git", push: true }, { name: "backup", url: "/backups/docs.git", push: true }] : [] }, projectRevision, sync: { backend, branch: backend === "git" ? "main" : "", head: backend === "git" ? "abc123" : "", state: "ready", dirty: false }, remotes: backend === "git" ? [{ name: "origin", state: "ready", head: "abc123", lastPushedHead: "abc123", branch: "main", retryable: false }, { name: "backup", state: "pending", head: "abc123", lastPushedHead: "", branch: "main", retryable: true }] : [] });

function environment(options = {}) {
  const { document, window } = parseHTML(html);
  Object.defineProperty(document, "baseURI", { value: "https://docs.test/team/_docs/admin/" });
  document.querySelector('meta[name="csp-nonce"]').setAttribute("content", "unit-test-nonce");
  const queries = new Map(); const confirmations = [];
  window.matchMedia = query => { if (!queries.has(query)) { const item = new EventTarget(); item.matches = false; queries.set(query, item); } return queries.get(query); };
  window.confirm = message => { confirmations.push(message); return options.confirm !== false; };
  const dialog = document.getElementById("action-dialog"); dialog.showModal = () => { dialog.open = true; }; dialog.close = () => { dialog.open = false; };
  // Linkedom omits the HTMLSelectElement.value setter implemented by browsers.
  Object.defineProperty(window.HTMLSelectElement.prototype, "value", { configurable: true, get() { return [...this.options].find(item => item.selected)?.value || this.options[0]?.value || ""; }, set(value) { for (const item of this.options) item.selected = item.value === String(value); } });
  const requests = [];
  let session = options.anonymous ? { user: null } : { user: { ...admin, ...(options.user || {}) }, csrfToken: "csrf-memory" };
  let draft = null, conflict = false, storage = options.storage || storageSnapshot(), storageSaves = 1;
  let settings = { revision: "s1", title: "Docs", description: "Description", logo: "", favicon: "", footer: "Footer", themeId: "paper", theme: { id: "paper", name: "Paper", mode: "auto", accent: "#315be8", font: "system", density: "comfortable", width: "normal", radius: 10 }, navigation: { showTOC: true, showPreviousNext: false, stickyHeader: true }, editor: { defaultFormat: "markdown", previewOnSave: false }, api: { enabled: true, showCurl: true, requestTimeoutMs: 18000, maxResponseBytes: 65536 }, publishing: { autoPublish: false, gitPushOnPublish: true } };
  if (options.settings) settings = { ...settings, ...options.settings };
  const fetcher = async (url, request) => {
    const route = url.pathname.replace("/team/_docs/api/v1/", "");
    const body = request.body && typeof request.body === "string" && request.headers["Content-Type"] === "application/json" ? JSON.parse(request.body) : request.body;
    requests.push({ route, search: url.search, method: request.method, body, headers: request.headers, credentials: request.credentials });
    if (options.respond) { const result = await options.respond({ route, search: url.search, request, body }); if (result !== undefined) return result; }
    if (route === "session") {
      if (request.method === "POST") session = { user: admin, csrfToken: "new-csrf" };
      if (request.method === "DELETE") { session = { user: null }; return json(null, 204); }
      return json(session);
    }
    if (route === "documents" && url.searchParams.get("published") === "true") return json({ documents: [{ ...source, content: undefined }, { ...source, id: "reference", title: "Reference", path: "docs/reference.md", content: undefined }], revision: settings.revision });
    if (route === "documents") return request.method === "POST" ? json({ ...source, ...body, id: "new-page", draft: true }, 201) : json({ documents: [{ ...source, content: undefined }], revision: "p1" });
    if (route === "documents/guide") {
      if (request.method === "PUT") {
        if (conflict) return json({ error: { code: "conflict", message: "A new revision exists", currentRevision: "p2" } }, 409);
        draft = { id: "draft-1", revision: "d1", baseRevision: "p1", path: source.path, operation: "write", content: body.content };
        return json({ revision: "p1", published: !!body.publish, draft, document: { ...source, content: body.content, draft: !body.publish, draftId: "draft-1", draftRevision: "d1", baseRevision: "p1" } });
      }
      return json({ ...source, ...(conflict && url.searchParams.has("published") ? { revision: "p2", publishedRevision: "p2" } : {}), ...(draft && !url.searchParams.has("published") ? { content: draft.content, draft: true, draftId: draft.id, draftRevision: draft.revision, baseRevision: draft.baseRevision } : {}) });
    }
    if (route === "documents/guide/rebase") { draft = { id: "rebased", revision: "d2", baseRevision: body.revision, path: source.path, operation: "write", content: body.content }; return json({ revision: body.revision, published: false, draft, document: { ...source, content: body.content, revision: body.revision, draft: true } }); }
    if (route === "preview") return json({ html: '<h1>Preview</h1><script>top.attack()</script>', diagnostics: [] });
    if (route === "settings") { if (request.method === "PUT") settings = { ...body, revision: "s2" }; return json(settings); }
    if (route === "themes") return request.method === "POST" ? json(body) : json({ themes: [{ ...settings.theme, builtin: true }, { ...settings.theme, id: "custom", name: "Custom", builtin: false, revision: "theme-r1" }] });
    if (route === "users") return request.method === "POST" ? json({ id: "u2", ...body }, 201) : json({ users: [admin, { id: "u2", username: "reader", displayName: "Reader", role: "viewer", disabled: false, grants: { read: ["docs"], write: [] } }] });
    if (route.startsWith("users/")) return request.method === "DELETE" ? json(null, 204) : json({ id: "u2", username: "reader", ...body });
    if (route === "files") return request.method === "POST" ? json({ revision: "p1", published: false, draft: { id: "dir", revision: "d2", baseRevision: "p1", path: body.path, operation: "mkdir" } }, 201) : json({ files: options.files || [{ path: "assets/image.png", name: "image.png", directory: false, size: 128, revision: "p1", canWrite: true }], revision: "p1" });
    if (route === "drafts") return json({ drafts: options.drafts || (draft ? [draft] : []) });
    if (route === "storage/configuration") {
      if (request.method === "PUT") { const enrolling = storage.settings.backend === "local" && body.backend === "git"; storage = { ...storage, settings: { revision: "c" + (++storageSaves), backend: body.backend, pullRemote: body.pullRemote, remotes: body.remotes }, projectRevision: enrolling ? "p2" : storage.projectRevision, sync: { ...storage.sync, backend: body.backend, branch: body.backend === "git" ? body.initialBranch || "main" : "" } }; }
      return json(storage);
    }
    if (route.startsWith("storage/remotes/")) return json({ name: route.split("/")[2], state: "ready", retryable: false, branch: "main", head: "abc123", lastPushedHead: "abc123" });
    if (route === "sync/status") return json({ backend: "git", branch: "main", revision: "p1", ahead: 1, behind: 0, pendingPush: true });
    if (route === "branches") return request.method === "POST" ? json({ name: body.name }, 201) : json({ branches: [{ name: "main", current: true }, { name: "drafts", current: false }] });
    if (route === "sync" || route === "branches/checkout") return json({ backend: "git", revision: "p1", branch: body.name || "main" });
    if (route === "documents/guide/history") return json({ revisions: [{ id: "h1", path: source.path, revision: "p0", message: "Initial guide", author: "Author", createdAt: "2026-10-28T00:00:00Z" }] });
    if (route === "documents/guide/restore") return json({ revision: "p1", published: false, document: source, draft: { id: "draft-restore", revision: "d-restore", baseRevision: "p1", path: source.path, operation: "write" } });
    return json({ revision: "p1", published: false });
  };
  const app = boot({ document, window, fetcher, loadEditor: options.loadEditor || (async () => { throw new Error("Use the source fallback in DOM unit tests"); }) });
  return { app, document, window, requests, confirmations, setConflict: value => { conflict = value; }, event: (target, name) => { target.dispatchEvent(new window.Event(name, { bubbles: true, cancelable: true })); } };
}

test("settings preserve local success and show pending Git synchronization without echoing response metadata", async () => {
  let saves = 0;
  const env = environment({ respond({ route, request, body }) {
    if (route === "settings" && request.method === "PUT") return json({ ...body, revision: "s" + (++saves + 1), sync: { backend: "git", state: "pending", retryable: true, error: "The local commit is retained; push failed." } });
  } });
  await env.app.ready; await env.app.navigate("settings");
  const form = env.document.querySelector(".settings-form"); env.event(form, "submit"); await settle();
  assert.match(env.document.getElementById("message").textContent, /saved locally/i); assert.match(env.document.getElementById("message").textContent, /push failed/);
  env.event(form, "submit"); await settle();
  const requests = env.requests.filter(item => item.route === "settings" && item.method === "PUT");
  assert.equal(requests.length, 2); assert.equal(requests[1].body.revision, "s2"); assert.equal(Object.hasOwn(requests[1].body, "sync"), false); env.app.dispose();
});

test("theme editor saves independent optional light and dark palettes with registry CAS", async () => {
  const custom = { id: "custom", name: "Custom", mode: "auto", revision: "theme-r1", builtin: false, light: { background: "#fafafa", text: "#172033" }, dark: { background: "#101622", text: "#eeeeee" } };
  const env = environment({ respond({ route, request }) { if (route === "themes" && request.method === "GET") return json({ themes: [custom] }); } });
  await env.app.ready; await env.app.navigate("appearance"); env.event(controls(env.document, "Edit")[0], "click");
  const form = env.document.getElementById("dialog-form");
  assert.equal(form.querySelector('[name="light.background"]').value, "#fafafa");
  form.querySelector('[name="light.background"]').value = "#ffffff";
  form.querySelector('[name="dark.background"]').value = "";
  form.querySelector('[name="dark.accent"]').value = "#aabbcc";
  env.event(form, "submit"); await settle();
  const request = env.requests.find(item => item.route === "themes" && item.method === "POST");
  assert.equal(request.body.revision, "theme-r1"); assert.equal(request.body.light.background, "#ffffff"); assert.equal(request.body.light.text, "#172033");
  assert.equal(Object.hasOwn(request.body.dark, "background"), false); assert.equal(request.body.dark.accent, "#aabbcc");
  assert.equal(Object.hasOwn(request.body, "light.background"), false); assert.equal(Object.hasOwn(request.body, "builtin"), false); env.app.dispose();
});

test("administration appearance follows the selected palette when system color mode changes", async () => {
  const theme = { id: "custom", name: "Custom", mode: "auto", accent: "#315be8", light: { background: "#fafafa", text: "#172033" }, dark: { background: "#101622", text: "#eeeeee" } };
  const env = environment({ respond({ route, request }) { if (route === "settings" && request.method === "GET") return json({ revision: "s1", title: "Docs", themeId: "custom", theme }); } });
  await env.app.ready;
  assert.equal(env.document.documentElement.style.getPropertyValue("--bg"), "#fafafa");
  const query = env.window.matchMedia("(prefers-color-scheme: dark)"); query.matches = true; query.dispatchEvent(new Event("change"));
  assert.equal(env.document.documentElement.style.getPropertyValue("--bg"), "#101622"); assert.equal(env.document.documentElement.style.getPropertyValue("--text"), "#eeeeee"); env.app.dispose();
});

test("anonymous login clears password before sending and uses session CSRF without persistent storage", async () => {
  const env = environment({ anonymous: true }); await env.app.ready;
  const form = env.document.getElementById("login-form"); form.querySelector('[name="username"]').value = "admin"; const password = form.querySelector('[name="password"]'); password.value = "a long private password";
  env.event(form, "submit"); assert.equal(password.value, ""); await settle();
  assert.equal(env.app.user.username, "admin"); assert.equal(env.app.api.csrfToken, "new-csrf"); assert.equal(env.document.getElementById("app").hidden, false);
  const request = env.requests.find(item => item.route === "session" && item.method === "POST"); assert.equal(request.body.password, "a long private password"); assert.equal(request.credentials, "same-origin");
  assert.equal(env.document.getElementById("document-tree").querySelector("img"), null, "titles render as text, never author HTML"); env.app.dispose();
});

test("document editing saves drafts with opaque revisions and previews in a script-blocking sandbox", async () => {
  const env = environment(); await env.app.ready; await env.app.openDocument("guide");
  const text = env.document.querySelector(".source-editor"); assert.equal(text.value, source.content); text.value = "# Local draft\n"; env.event(text, "input"); assert.equal(env.app.workspace.dirty, true);
  env.event(controls(env.document, "Save draft")[0], "click"); await settle();
  const saved = env.requests.find(item => item.route === "documents/guide" && item.method === "PUT"); assert.equal(saved.body.content, "# Local draft\n"); assert.equal(saved.body.revision, "p1"); assert.equal(saved.body.publish, false); assert.equal(saved.headers["X-CSRF-Token"], "csrf-memory"); assert.equal(env.app.workspace.dirty, false);
  env.event(controls(env.document, "Preview")[0], "click"); await settle(); const frame = env.document.querySelector("iframe"); assert.ok(frame); assert.equal(frame.getAttribute("sandbox"), "allow-same-origin"); assert.equal(frame.getAttribute("sandbox").includes("allow-scripts"), false); assert.equal(frame.getAttribute("allow"), null); assert.match(frame.srcdoc, /default-src 'none'/); assert.equal(env.document.querySelector("#preview-host script"), null); env.app.dispose();
});

test("revision conflicts keep the source editable and expose readonly comparison without forced overwrite", async () => {
  const env = environment(); await env.app.ready; await env.app.openDocument("guide"); const text = env.document.querySelector(".source-editor"); text.value = "my unsaved draft"; env.event(text, "input"); env.setConflict(true);
  env.event(controls(env.document, "Publish")[0], "click"); await settle(); assert.equal(env.app.workspace.content, "my unsaved draft"); assert.equal(env.app.workspace.dirty, true); assert.ok(controls(env.document, "Compare latest").length);
  env.event(controls(env.document, "Compare latest")[0], "click"); await settle(); const comparison = env.document.querySelector('[aria-label="Latest published source"]'); assert.equal(comparison.value, source.content); assert.equal(comparison.readOnly, true); assert.equal(env.app.workspace.content, "my unsaved draft");
  assert.ok(env.requests.some(item => item.route === "documents/guide" && item.search === "?published=true")); env.app.dispose();
});

test("viewers get readonly source without mutation or administrator controls", async () => {
  const env = environment({ user: { role: "viewer" } }); await env.app.ready; await env.app.openDocument("guide");
  assert.equal(env.document.querySelector(".source-editor").readOnly, true); assert.equal(controls(env.document, "Save draft").length, 0); assert.equal(controls(env.document, "Publish").length, 0);
  assert.equal(env.document.querySelector('[data-view="people"]').hidden, true); await assert.rejects(env.app.navigate("people"), error => error.status === 403); env.app.dispose();
});

test("settings round-trip preserves nested unrelated values and sends the current settings revision", async () => {
  const env = environment(); await env.app.ready; await env.app.navigate("settings");
  const form = env.document.querySelector(".settings-form"); form.querySelector('[name="title"]').value = "My handbook"; env.event(form, "input"); env.event(form, "submit"); await settle();
  const request = env.requests.find(item => item.route === "settings" && item.method === "PUT"); assert.equal(request.body.revision, "s1"); assert.equal(request.body.title, "My handbook"); assert.equal(request.body.navigation.showPreviousNext, false); assert.equal(request.body.publishing.gitPushOnPublish, true); assert.equal(request.body.api.maxResponseBytes, 65536); assert.equal(request.body.theme.accent, "#315be8"); env.app.dispose();
});

test("people flow clears password and serializes explicit grants without granting viewer write capability", async () => {
  const env = environment(); await env.app.ready; await env.app.navigate("people"); env.event(controls(env.document, "Create account")[0], "click");
  const form = env.document.getElementById("dialog-form"); form.querySelector('[name="username"]').value = "writer"; form.querySelector('[name="displayName"]').value = "Writer"; form.querySelector('[name="role"]').value = "viewer"; const password = form.querySelector('[name="password"]'); password.value = "a correct long password"; form.querySelector('[name="customGrants"]').checked = true; form.querySelector('[name="readPrefixes"]').value = "docs\ndocs\n"; form.querySelector('[name="writePrefixes"]').value = "docs/private";
  form.onsubmit({ preventDefault() {} }); assert.equal(password.value, ""); await settle();
  const request = env.requests.find(item => item.route === "users" && item.method === "POST"); assert.deepEqual(request.body.grants, { read: ["docs"], write: ["docs/private"] }); assert.equal(request.body.role, "viewer"); assert.equal(request.body.password, "a correct long password"); env.app.dispose();
});

test("repository actions carry revision protection and folder creation is an explicit draft", async () => {
  const env = environment(); await env.app.ready; await env.app.navigate("publishing"); env.event(controls(env.document, "Push commits")[0], "click"); await settle();
  const push = env.requests.find(item => item.route === "sync" && item.method === "POST"); assert.deepEqual(push.body, { action: "push", revision: "p1" });
  await env.app.navigate("files"); env.event(controls(env.document, "New folder")[0], "click"); const form = env.document.getElementById("dialog-form"); form.querySelector('[name="path"]').value = "assets/new"; await form.onsubmit({ preventDefault() {} });
  const folder = env.requests.find(item => item.route === "files" && item.method === "POST"); assert.deepEqual(folder.body, { path: "assets/new", revision: "p1" });
  const download = [...env.document.querySelectorAll("a")].find(node => node.textContent === "Download"); assert.equal(download.getAttribute("href"), "https://docs.test/team/_docs/api/v1/files/content?path=assets%2Fimage.png"); env.app.dispose();
});

test("canceling dirty navigation keeps document and blocks logout instead of silently discarding", async () => {
  const env = environment({ confirm: false }); await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("keep this draft");
  env.event(env.document.getElementById("logout"), "click"); await settle(); assert.equal(env.app.workspace.content, "keep this draft"); assert.equal(env.app.user.id, admin.id); assert.equal(env.requests.some(item => item.method === "DELETE" && item.route === "session"), false); assert.equal(env.confirmations.length, 1); env.app.dispose();
});

test("manual merge remains visible during editing and rebases only after an explicit reviewed action", async () => {
  const env = environment(); await env.app.ready; await env.app.openDocument("guide"); const text = env.document.querySelector(".source-editor"); text.value = "my local draft"; env.event(text, "input"); env.setConflict(true);
  env.event(controls(env.document, "Publish")[0], "click"); await settle(); const use = controls(env.document, "Use my merged draft")[0]; assert.equal(use.hidden, true);
  env.event(controls(env.document, "Compare latest")[0], "click"); await settle(); assert.equal(use.hidden, false);
  const comparison = env.document.querySelector('[aria-label="Latest published source"]'); text.value = "manually merged content"; env.event(text, "input"); assert.equal(env.document.querySelector('[aria-label="Latest published source"]'), comparison);
  env.event(use, "click"); await settle(); const request = env.requests.find(item => item.route === "documents/guide/rebase"); assert.equal(request.body.content, "manually merged content"); assert.equal(request.body.revision, "p2"); assert.equal(env.confirmations.length, 1); assert.equal(env.app.workspace.document.baseRevision, "p2"); assert.equal(env.app.workspace.content, "manually merged content"); assert.equal(env.app.workspace.conflict, null); env.app.dispose();
});

test("settings can be saved repeatedly without reusing an obsolete revision", async () => {
  const env = environment(); await env.app.ready; await env.app.navigate("settings"); const form = env.document.querySelector(".settings-form"); const title = form.querySelector('[name="title"]'); title.value = "First title"; env.event(form, "submit"); await settle(); title.value = "Second title"; env.event(form, "submit"); await settle();
  const requests = env.requests.filter(item => item.route === "settings" && item.method === "PUT"); assert.equal(requests.length, 2); assert.equal(requests[0].body.revision, "s1"); assert.equal(requests[1].body.revision, "s2"); assert.equal(requests[1].body.title, "Second title"); env.app.dispose();
});

test("a failed rich-editor constructor leaves the original source editor usable", async () => {
  const env = environment({ loadEditor: async () => ({ createEditor() { throw new Error("Editor not available"); } }) }); await env.app.ready; await env.app.openDocument("guide"); const text = env.document.querySelector(".source-editor"); assert.ok(text); assert.equal(text.value, source.content); text.value = "fallback edits"; env.event(text, "input"); assert.equal(env.app.workspace.content, "fallback edits"); env.app.dispose();
});

test("a late folder response cannot replace the newer folder selection", async () => {
  let finishAlpha;
  const alpha = new Promise(resolve => { finishAlpha = resolve; });
  const env = environment({ respond({ route, search }) {
    if (route !== "files") return;
    const path = new URLSearchParams(search).get("path");
    if (path === ".") return json({ revision: "p1", files: ["alpha", "beta"].map(name => ({ path: name, name, directory: true, revision: "p1" })) });
    if (path === "alpha") return alpha;
    if (path === "beta") return json({ revision: "p1", files: [{ path: "beta/new.txt", name: "new.txt", directory: false, revision: "p1" }] });
  } });
  await env.app.ready; await env.app.navigate("files"); env.event(controls(env.document, "alpha/")[0], "click"); env.event(controls(env.document, "beta/")[0], "click"); await settle(); finishAlpha(json({ revision: "p1", files: [{ path: "alpha/old.txt", name: "old.txt", directory: false, revision: "p1" }] })); await settle();
  assert.match(env.document.getElementById("view").textContent, /new\.txt/); assert.doesNotMatch(env.document.getElementById("view").textContent, /old\.txt/); env.app.dispose();
});

test("theme selection sends only the selected theme and preserves broad settings", async () => {
  const env = environment(); await env.app.ready; await env.app.navigate("appearance");
  const custom = [...env.document.querySelectorAll(".theme-card")].find(card => card.querySelector("h3").textContent === "Custom"); env.event([...custom.querySelectorAll("button")].find(node => node.textContent === "Use theme"), "click"); await settle();
  const request = env.requests.find(item => item.route === "settings" && item.method === "PUT"); assert.equal(request.body.themeId, "custom"); assert.equal(request.body.theme.id, "custom"); assert.equal(Object.hasOwn(request.body.theme, "builtin"), false); assert.equal(Object.hasOwn(request.body.theme, "revision"), false); assert.equal(request.body.publishing.gitPushOnPublish, true); assert.equal(request.body.revision, "s1"); env.app.dispose();
});

test("attachment uploads keep original bytes, quote revision and report staged changes accurately", async () => {
  const env = environment(); await env.app.ready; await env.app.navigate("files"); const upload = env.document.querySelector('input[type="file"]'); const file = new File([new Uint8Array([0, 255, 1])], "sample.pdf", { type: "application/pdf" }); Object.defineProperty(upload, "files", { value: [file] }); env.event(upload, "change"); await settle();
  const request = env.requests.find(item => item.route === "files/content" && item.method === "PUT"); assert.equal(request.body, file); assert.deepEqual([...new Uint8Array(await request.body.arrayBuffer())], [0, 255, 1]); assert.equal(request.headers["If-Match"], '\"p1\"'); assert.equal(request.headers["Content-Type"], "application/octet-stream"); assert.match(env.document.getElementById("message").textContent, /1 file\(s\) saved as drafts; 0 file\(s\) published/); env.app.dispose();
});

test("storage is administrator-only and loads atomic local status without remote traffic or credential inputs", async () => {
  for (const role of ["viewer", "editor"]) {
    const env = environment({ user: { role } }); await env.app.ready;
    assert.equal(env.document.querySelector('[data-view="storage"]').hidden, true);
    await assert.rejects(env.app.navigate("storage"), error => error.status === 403);
    assert.equal(env.requests.some(item => item.route.startsWith("storage/")), false); env.app.dispose();
  }
  const env = environment({ storage: storageSnapshot("local") }); await env.app.ready;
  assert.equal(env.requests.some(item => item.route.startsWith("storage/")), false);
  await env.app.navigate("storage");
  assert.equal(env.document.getElementById("view-title").textContent, "Storage");
  assert.match(env.document.getElementById("view").textContent, /Local files/);
  assert.equal(env.requests.filter(item => item.route.startsWith("storage/")).length, 1);
  assert.equal(env.document.querySelector('.storage-form input[type="password"]'), null);
  assert.equal(env.document.querySelector('.storage-form [name="privateKey"]'), null);
  assert.match(env.document.getElementById("view").textContent, /known_hosts/); env.app.dispose();
});

test("storage controls follow full-project read/write grants without changing other admin sections", async () => {
  for (const scenario of [
    { name: "viewer", user: { role: "viewer" }, allowed: false },
    { name: "editor", user: { role: "editor" }, allowed: false },
    { name: "scoped read admin", user: { role: "admin", grants: { read: ["docs"], write: ["."] } }, allowed: false },
    { name: "scoped write admin", user: { role: "admin", grants: { read: ["."], write: ["docs"] } }, allowed: false },
    { name: "unrestricted admin", user: { role: "admin" }, allowed: true },
    { name: "explicit root grants admin", user: { role: "admin", grants: { read: ["."], write: ["."] } }, allowed: true },
  ]) {
    const env = environment({ user: scenario.user }); await env.app.ready;
    assert.equal(env.document.querySelector('[data-view="storage"]').hidden, !scenario.allowed, scenario.name);
    for (const section of ["people", "settings", "appearance"]) assert.equal(env.document.querySelector('[data-view="' + section + '"]').hidden, scenario.user.role !== "admin", scenario.name + ": " + section);
    await env.app.navigate("publishing");
    assert.equal(controls(env.document, "Manage storage connections").length, scenario.allowed ? 1 : 0, scenario.name);
    if (scenario.allowed) { await env.app.navigate("storage"); assert.ok(env.document.querySelector(".storage-form"), scenario.name); }
    else { await assert.rejects(env.app.navigate("storage"), error => error.status === 403, scenario.name); assert.equal(env.requests.some(item => item.route.startsWith("storage/")), false, scenario.name); }
    env.app.dispose();
  }
});

test("Git enrollment confirms history changes and retains dirty editor text, document identity and saved-draft CAS", async () => {
  const env = environment({ storage: storageSnapshot("local"), confirm: false }); await env.app.ready; await env.app.openDocument("guide");
  env.app.workspace.document = { ...env.app.workspace.document, draft: true, draftId: "retained-draft", draftRevision: "d8", baseRevision: "p1" };
  const document = env.app.workspace.document; env.app.workspace.edit("retain this unsaved source");
  await env.app.navigate("storage"); const form = env.document.querySelector(".storage-form");
  form.querySelector('[name="storageBackend"]').value = "git"; env.event(form, "change"); env.event(form, "submit"); await settle();
  assert.equal(env.requests.some(item => item.route === "storage/configuration" && item.method === "PUT"), false);
  assert.equal(env.app.workspace.content, "retain this unsaved source");
  assert.match(env.confirmations[0], /history.*revision/); assert.match(env.confirmations[0], /retained/);
  env.window.confirm = () => true; env.event(form, "submit"); await settle();
  const request = env.requests.find(item => item.route === "storage/configuration" && item.method === "PUT");
  assert.deepEqual(request.body, { revision: "c1", baseRevision: "p1", backend: "git", initialBranch: "main", pullRemote: "", remotes: [] });
  assert.equal(env.app.workspace.document, document); assert.equal(env.app.workspace.document.draftRevision, "d8");
  assert.equal(env.app.workspace.content, "retain this unsaved source"); assert.equal(env.app.workspace.dirty, true);
  assert.match(env.document.getElementById("message").textContent, /drafts were retained/);
  await env.app.navigate("documents"); assert.equal(env.document.querySelector(".source-editor").value, "retain this unsaved source"); env.app.dispose();
});

test("connection settings use independent settings and project revisions and refresh CAS after each save", async () => {
  const env = environment(); await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("unsaved while configuring"); const document = env.app.workspace.document;
  await env.app.navigate("storage"); let form = env.document.querySelector(".storage-form");
  const rows = [...form.querySelectorAll("[data-storage-remote]")]; rows[0].querySelector('[name="remoteURL"]').value = "ssh://git@primary.example/team/docs.git";
  env.event(rows[1].querySelector('input[type="radio"]'), "change"); env.event(form, "submit"); await settle();
  let requests = env.requests.filter(item => item.route === "storage/configuration" && item.method === "PUT");
  assert.equal(requests[0].body.revision, "c1"); assert.equal(requests[0].body.baseRevision, "p1"); assert.equal(requests[0].body.pullRemote, "backup");
  assert.equal(requests[0].body.remotes[0].url, "ssh://git@primary.example/team/docs.git"); assert.equal(Object.hasOwn(requests[0].body, "initialBranch"), false);
  form = env.document.querySelector(".storage-form"); assert.equal(form.querySelector('option[value="local"]').disabled, true);
  env.event(form, "submit"); await settle(); requests = env.requests.filter(item => item.route === "storage/configuration" && item.method === "PUT");
  assert.equal(requests[1].body.revision, "c2"); assert.equal(requests[1].body.baseRevision, "p1");
  assert.equal(env.app.workspace.document, document); assert.equal(env.app.workspace.content, "unsaved while configuring"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("explicit per-remote checks and push retries preserve the form and independent target outcomes", async () => {
  let backupAttempts = 0;
  const env = environment({ respond({ route }) {
    if (route === "storage/remotes/backup/push") return json({ name: "backup", state: ++backupAttempts === 1 ? "error" : "ready", retryable: backupAttempts === 1, error: backupAttempts === 1 ? "Remote push failed; local commit retained." : "", head: "abc123", lastPushedHead: backupAttempts === 1 ? "" : "abc123", branch: "main" });
  } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("keep editor state"); await env.app.navigate("storage");
  const form = env.document.querySelector(".storage-form"); const address = form.querySelector('[name="remoteURL"]'); address.value = "git@edited.example:docs.git"; env.event(address, "input");
  env.event(env.document.querySelector('[aria-label="Check connection origin"]'), "click"); await settle();
  env.event(env.document.querySelector('[aria-label="Push commits to backup"]'), "click"); await settle();
  assert.match(env.document.querySelector('[data-remote-status="backup"]').textContent, /Failed/);
  assert.match(env.document.getElementById("message").textContent, /failed.*local commit retained/);
  assert.match(env.document.querySelector('[data-remote-status="origin"]').textContent, /Connection ready/);
  assert.equal(env.document.querySelector(".storage-form"), form); assert.equal(address.value, "git@edited.example:docs.git");
  const check = env.requests.find(item => item.route === "storage/remotes/origin/check"); assert.deepEqual(check.body, { revision: "c1" });
  const push = env.requests.find(item => item.route === "storage/remotes/backup/push"); assert.deepEqual(push.body, { revision: "c1", baseRevision: "p1" }); assert.equal(push.headers["X-CSRF-Token"], "csrf-memory");
  env.event(env.document.querySelector('[aria-label="Push commits to backup"]'), "click"); await settle();
  assert.match(env.document.querySelector('[data-remote-status="backup"]').textContent, /Connection ready/); assert.match(env.document.getElementById("message").textContent, /completed/);
  assert.equal(backupAttempts, 2); assert.equal(env.app.workspace.content, "keep editor state"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("storage conflicts retain edits and require reviewing new revisions before an explicit rebase", async () => {
  let reads = 0, writes = 0;
  const env = environment({ respond({ route, request, body }) {
    if (route !== "storage/configuration") return;
    if (request.method === "GET") return json(storageSnapshot("git", ++reads === 1 ? "c1" : "c2", reads === 1 ? "p1" : "p2"));
    if (++writes === 1) return json({ error: { code: "conflict", message: "Storage settings changed elsewhere" } }, 409);
    return json({ ...storageSnapshot("git", "c3", "p2"), settings: { revision: "c3", backend: body.backend, pullRemote: body.pullRemote, remotes: body.remotes } });
  } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("my unsaved document"); await env.app.navigate("storage");
  const form = env.document.querySelector(".storage-form"), address = form.querySelector('[name="remoteURL"]'); address.value = "git@my.example:docs.git"; env.event(address, "input"); env.event(form, "submit"); await settle();
  assert.equal(env.document.querySelector(".storage-form"), form); assert.equal(address.value, "git@my.example:docs.git"); assert.match(env.document.querySelector('#view [role="alert"]').textContent, /retained/);
  env.event(controls(env.document, "Refresh storage status")[0], "click"); await settle();
  assert.equal(env.document.querySelector(".storage-form"), form); assert.equal(address.value, "git@my.example:docs.git");
  const comparison = env.document.querySelector('[aria-label="Storage revision conflict"]'); assert.equal(comparison.hidden, false); assert.match(comparison.textContent, /c2/);
  assert.equal(form.querySelector('button[type="submit"]').disabled, true);
  env.event(controls(env.document, "Use reviewed revisions")[0], "click"); env.event(form, "submit"); await settle();
  const request = env.requests.filter(item => item.route === "storage/configuration" && item.method === "PUT")[1];
  assert.equal(request.body.revision, "c2"); assert.equal(request.body.baseRevision, "p2"); assert.equal(request.body.remotes[0].url, "git@my.example:docs.git");
  assert.equal(env.app.workspace.content, "my unsaved document"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("late storage loads and refreshes cannot replace another view or newer saved settings", async () => {
  let finishInitial;
  const pending = new Promise(resolve => { finishInitial = resolve; });
  const env = environment({ respond({ route, request }) { if (route === "storage/configuration" && request.method === "GET") return pending; } });
  await env.app.ready; const navigation = env.app.navigate("storage"); await settle(); await env.app.navigate("settings");
  const settingsForm = env.document.querySelector(".settings-form"); finishInitial(json(storageSnapshot())); await navigation;
  assert.equal(env.document.querySelector(".settings-form"), settingsForm); assert.equal(env.document.querySelector(".storage-form"), null); env.app.dispose();

  let finishRefresh, reads = 0;
  const refresh = new Promise(resolve => { finishRefresh = resolve; });
  const other = environment({ respond({ route, request }) { if (route === "storage/configuration" && request.method === "GET" && ++reads === 2) return refresh; } });
  await other.app.ready; await other.app.navigate("storage"); other.event(controls(other.document, "Refresh storage status")[0], "click"); await settle();
  const form = other.document.querySelector(".storage-form"); form.querySelector('[name="remoteURL"]').value = "git@new.example:docs.git"; other.event(form, "submit"); await settle();
  finishRefresh(json(storageSnapshot("git", "obsolete", "obsolete-project"))); await settle();
  assert.match(other.document.getElementById("view").textContent, /c2/); assert.doesNotMatch(other.document.getElementById("view").textContent, /obsolete/);
  assert.equal(other.document.querySelector('.storage-form [name="remoteURL"]').value, "git@new.example:docs.git"); other.app.dispose();
});

test("storage connection limit, removal and canceled form navigation keep the user's entries", async () => {
  const env = environment({ confirm: false }); await env.app.ready; await env.app.navigate("storage");
  for (let i = 0; i < 6; i++) env.event(controls(env.document, "Add connection")[0], "click");
  assert.equal(env.document.querySelectorAll("[data-storage-remote]").length, 8); assert.equal(controls(env.document, "Add connection")[0].disabled, true);
  const form = env.document.querySelector(".storage-form"); await env.app.navigate("settings"); assert.equal(env.document.querySelector(".storage-form"), form);
  env.event(controls(env.document, "Remove connection")[0], "click"); assert.equal(env.document.querySelectorAll("[data-storage-remote]").length, 7); assert.equal(controls(env.document, "Add connection")[0].disabled, false);
  env.event(controls(env.document, "Reload saved settings")[0], "click"); await settle(); assert.equal(env.document.querySelector(".storage-form"), form);
  assert.equal(env.requests.filter(item => item.route === "storage/configuration").length, 1); env.app.dispose();
});

test("recoverable storage load failures and push HTTP failures retain the editor and entered configuration", async () => {
  let reads = 0;
  const env = environment({ respond({ route, request }) {
    if (route === "storage/configuration" && request.method === "GET" && ++reads === 1) return json({ error: { code: "busy", message: "Storage is busy. Try again." } }, 503);
    if (route === "storage/remotes/backup/push") return json({ error: { code: "busy", message: "Connection slot is busy. Retry later." } }, 503);
  } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("retained through failures"); await env.app.navigate("storage");
  assert.equal(env.document.querySelector(".storage-form"), null); assert.match(env.document.querySelector('#view [role="alert"]').textContent, /busy/);
  env.event(controls(env.document, "Refresh storage status")[0], "click"); await settle();
  const form = env.document.querySelector(".storage-form"), address = form.querySelector('[name="remoteURL"]'); address.value = "git@retained.example:docs.git"; env.event(address, "input");
  env.event(env.document.querySelector('[aria-label="Push commits to backup"]'), "click"); await settle();
  assert.match(env.document.querySelector('[data-remote-status="backup"]').textContent, /Failed.*slot is busy/s);
  assert.match(env.document.querySelector('[data-remote-status="origin"]').textContent, /Connection ready/);
  assert.equal(env.document.querySelector(".storage-form"), form); assert.equal(address.value, "git@retained.example:docs.git");
  assert.equal(env.app.workspace.content, "retained through failures"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("pending push responses are not reported as completed and duplicate requests are gated", async () => {
  let finishPush;
  const pending = new Promise(resolve => { finishPush = resolve; });
  const env = environment({ respond({ route }) { if (route === "storage/remotes/backup/push") return pending; } });
  await env.app.ready; await env.app.navigate("storage");
  const push = env.document.querySelector('[aria-label="Push commits to backup"]'); env.event(push, "click"); env.event(push, "click"); await settle();
  assert.equal(env.requests.filter(item => item.route === "storage/remotes/backup/push").length, 1);
  assert.equal(env.document.querySelector('.storage-form [name="remoteURL"]').disabled, true);
  finishPush(json({ name: "backup", state: "pending", retryable: true, branch: "main" })); await settle();
  assert.match(env.document.getElementById("message").textContent, /remains pending/); assert.doesNotMatch(env.document.getElementById("message").textContent, /completed/);
  assert.equal(env.document.querySelector('.storage-form [name="remoteURL"]').disabled, false); env.app.dispose();
});

test("late successful storage saves do not replace a new view or clear that view's dirty form", async () => {
  let finishSave;
  const pending = new Promise(resolve => { finishSave = resolve; });
  const env = environment({ respond({ route, request }) { if (route === "storage/configuration" && request.method === "PUT") return pending; } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("still my editor text"); await env.app.navigate("storage");
  const storageForm = env.document.querySelector(".storage-form"); env.event(storageForm, "input"); env.event(storageForm, "submit"); await settle();
  await env.app.navigate("settings"); const settingsForm = env.document.querySelector(".settings-form"); settingsForm.querySelector('[name="title"]').value = "Unsaved new settings"; env.event(settingsForm, "input");
  env.window.confirm = () => false; finishSave(json(storageSnapshot("git", "c9", "p9"))); await settle();
  assert.equal(env.document.querySelector(".settings-form"), settingsForm); assert.equal(settingsForm.querySelector('[name="title"]').value, "Unsaved new settings");
  await env.app.navigate("documents"); assert.equal(env.document.querySelector(".settings-form"), settingsForm, "late storage success cannot clear another form's dirty flag");
  assert.equal(env.app.workspace.content, "still my editor text"); assert.doesNotMatch(env.document.getElementById("message").textContent, /Storage settings saved/); env.app.dispose();
});

test("explicit storage reload replaces only the connection form and failed settings responses keep it editable", async () => {
  const env = environment({ respond({ route, request }) { if (route === "storage/configuration" && request.method === "PUT") return json({ settings: { revision: "broken" } }); } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("keep my source"); await env.app.navigate("storage");
  const form = env.document.querySelector(".storage-form"), address = form.querySelector('[name="remoteURL"]'); address.value = "git@edit.example:docs.git"; env.event(address, "input"); env.event(form, "submit"); await settle();
  assert.equal(env.document.querySelector(".storage-form"), form); assert.equal(address.disabled, false); assert.equal(address.value, "git@edit.example:docs.git");
  assert.match(env.document.querySelector('#view [role="alert"]').textContent, /incomplete/);
  env.event(controls(env.document, "Reload saved settings")[0], "click"); await settle();
  assert.notEqual(env.document.querySelector(".storage-form"), form); assert.equal(env.document.querySelector('.storage-form [name="remoteURL"]').value, "git@example.com:team/docs.git");
  assert.equal(env.app.workspace.content, "keep my source"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("Publishing reports partial push targets as pending and keeps dirty source after a failed push", async () => {
  const env = environment({ respond({ route }) { if (route === "sync") return json({ backend: "git", pendingPush: true, lastError: "Backup connection failed; local commit retained." }); } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("unsaved source"); await env.app.navigate("publishing"); env.event(controls(env.document, "Push commits")[0], "click"); await settle();
  assert.match(env.document.getElementById("message").textContent, /did not finish.*Backup connection failed/);
  assert.doesNotMatch(env.document.getElementById("message").textContent, /synchronization completed/);
  assert.equal(env.app.workspace.content, "unsaved source"); assert.equal(env.app.workspace.dirty, true); assert.ok(controls(env.document, "Manage storage connections").length); env.app.dispose();
});

test("an active storage timeout is visible and retains edits instead of silently claiming success", async () => {
  const env = environment({ respond({ route }) { if (route === "storage/remotes/backup/push") throw new DOMException("Canceled", "AbortError"); } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("retained after timeout"); await env.app.navigate("storage");
  const form = env.document.querySelector(".storage-form"), address = form.querySelector('[name="remoteURL"]'); address.value = "git@timeout.example:docs.git"; env.event(address, "input");
  env.event(env.document.querySelector('[aria-label="Push commits to backup"]'), "click"); await settle();
  assert.match(env.document.querySelector('#view [role="alert"]').textContent, /timed out.*retained/);
  assert.match(env.document.querySelector('[data-remote-status="backup"]').textContent, /timed out/);
  assert.equal(env.document.querySelector(".storage-form"), form); assert.equal(address.value, "git@timeout.example:docs.git"); assert.equal(address.disabled, false);
  assert.equal(env.app.workspace.content, "retained after timeout"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("navigation editor requires root grants while scoped admins retain ordinary reading settings", async () => {
  for (const user of [{ role: "viewer" }, { role: "editor" }, { role: "admin", grants: { read: ["docs"], write: ["."] } }, { role: "admin", grants: { read: ["."], write: ["docs"] } }]) {
    const env = environment({ user }); await env.app.ready;
    assert.equal(env.document.querySelector('[data-view="navigation"]').hidden, true);
    await assert.rejects(env.app.navigate("navigation"), error => error.status === 403);
    assert.equal(env.requests.some(item => item.route === "documents" && item.search === "?published=true"), false);
    if (user.role === "admin") {
      await env.app.navigate("settings"); const form = env.document.querySelector(".settings-form"); assert.ok(form); assert.equal(controls(env.document, "Edit documentation navigation").length, 0);
      env.event(form, "submit"); await settle(); const put = env.requests.find(item => item.route === "settings" && item.method === "PUT"); assert.equal(Object.hasOwn(put.body.navigation, "tree"), false);
    }
    env.app.dispose();
  }
});

test("accessible navigation actions group, reorder, indent and outdent stable page identities", async () => {
  const env = environment(); await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("keep my editor draft"); const document = env.app.workspace.document;
  await env.app.navigate("navigation"); assert.ok(env.requests.some(item => item.route === "documents" && item.search === "?published=true"));
  env.event(controls(env.document, "Customize navigation")[0], "click"); env.event(controls(env.document, "Add section")[0], "click"); await settle();
  env.event(env.document.querySelector('[aria-label="Move up New section"]'), "click"); env.event(env.document.querySelector('[aria-label="Move up New section"]'), "click");
  const guide = env.document.querySelector('[data-navigation-page="guide"]'); env.event([...guide.querySelectorAll("button")].find(node => node.textContent === "Indent"), "click");
  const reference = env.document.querySelector('[data-navigation-page="reference"]'); env.event([...reference.querySelectorAll("button")].find(node => node.textContent === "Indent"), "click");
  let group = env.document.querySelector('[data-navigation-group="section-1"]');
  group.querySelector('[name="navigationGroupId"]').value = "guides"; env.event(group.querySelector('[name="navigationGroupId"]'), "input");
  group.querySelector('[name="navigationGroupTitle"]').value = "Guides"; env.event(group.querySelector('[name="navigationGroupTitle"]'), "input");
  assert.ok(group.querySelector('[aria-label="Move up Guides"]')); assert.ok([...env.document.querySelector('[name="navigationParent"]').options].some(option => option.textContent === "Guides"));
  group.querySelector('[name="navigationGroupDescription"]').value = "Read first\nThen explore"; env.event(group.querySelector('[name="navigationGroupDescription"]'), "input");
  group.querySelector('[name="navigationGroupCollapsed"]').checked = true; env.event(group.querySelector('[name="navigationGroupCollapsed"]'), "change");
  const alias = env.document.querySelector('[data-navigation-page="guide"] [name="navigationPageTitle"]'); alias.value = "Start here"; env.event(alias, "input");
  env.event(env.document.querySelector('[aria-label="Outdent Reference"]'), "click");
  env.event(env.document.querySelector('[aria-label="Move down Guides"]'), "click");
  const unlisted = env.document.querySelector('[name="navigationUnlisted"]'); unlisted.value = "omit"; env.event(unlisted, "change");
  env.event(env.document.querySelector(".navigation-form"), "submit"); await settle();
  const request = env.requests.find(item => item.route === "settings" && item.method === "PUT"); assert.equal(request.body.revision, "s1");
  assert.deepEqual(request.body.navigation.tree, { unlistedPages: "omit", items: [{ kind: "page", pageId: "reference" }, { kind: "group", id: "guides", title: "Guides", description: "Read first\nThen explore", collapsed: true, children: [{ kind: "page", pageId: "guide", title: "Start here" }] }] });
  assert.equal(request.body.navigation.showPreviousNext, false); assert.deepEqual(env.app.settings.navigation.tree, request.body.navigation.tree); assert.equal(request.body.api.maxResponseBytes, 65536); assert.equal(env.app.workspace.document, document); assert.equal(env.app.workspace.content, "keep my editor draft"); assert.equal(env.app.workspace.dirty, true);
  env.event(env.document.querySelector(".navigation-form"), "submit"); await settle(); const puts = env.requests.filter(item => item.route === "settings" && item.method === "PUT"); assert.equal(puts[1].body.revision, "s2"); env.app.dispose();
});

test("custom empty navigation survives mode toggles and explicit automatic reset sends null", async () => {
  const env = environment({ settings: { navigation: { showTOC: true, showPreviousNext: true, stickyHeader: true, tree: { unlistedPages: "omit", items: [] } } } });
  await env.app.ready; await env.app.navigate("navigation"); assert.match(env.document.querySelector(".navigation-form").textContent, /custom tree is empty/);
  env.event(controls(env.document, "Use automatic navigation")[0], "click"); env.event(controls(env.document, "Customize navigation")[0], "click");
  assert.equal(env.document.querySelectorAll("[data-navigation-page]").length, 0); assert.equal(env.document.querySelector('[name="navigationUnlisted"]').value, "omit");
  env.event(env.document.querySelector(".navigation-form"), "submit"); await settle();
  let puts = env.requests.filter(item => item.route === "settings" && item.method === "PUT"); assert.deepEqual(puts[0].body.navigation.tree, { unlistedPages: "omit", items: [] });
  env.event(controls(env.document, "Use automatic navigation")[0], "click"); env.event(env.document.querySelector(".navigation-form"), "submit"); await settle();
  puts = env.requests.filter(item => item.route === "settings" && item.method === "PUT"); assert.equal(puts[1].body.navigation.tree, null); assert.equal(puts[1].body.revision, "s2"); env.app.dispose();
});

test("empty sections fail without a request and document removal does not follow navigation removal", async () => {
  const env = environment({ confirm: false }); await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("unsaved source remains"); await env.app.navigate("navigation");
  env.event(controls(env.document, "Customize navigation")[0], "click"); env.event(controls(env.document, "Add section")[0], "click");
  const form = env.document.querySelector(".navigation-form"); env.event(form, "submit"); await settle(); assert.equal(env.requests.some(item => item.route === "settings" && item.method === "PUT"), false);
  assert.match(env.document.querySelector('#view [role="alert"]').textContent, /empty section/); assert.equal(env.document.querySelector(".navigation-form"), form);
  await env.app.navigate("settings"); assert.equal(env.document.querySelector(".navigation-form"), form);
  env.event(env.document.querySelector('[aria-label="Remove New section"]'), "click");
  env.event(env.document.querySelector('[data-navigation-page="reference"] button[aria-label="Remove Reference"]'), "click"); env.event(form, "submit"); await settle();
  const request = env.requests.find(item => item.route === "settings" && item.method === "PUT"); assert.deepEqual(request.body.navigation.tree.items, [{ kind: "page", pageId: "guide" }]);
  assert.equal(env.requests.some(item => item.method === "DELETE"), false); assert.equal(env.app.workspace.content, "unsaved source remains"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("navigation revision conflicts preserve the edited tree until an explicit reviewed rebase", async () => {
  let published = false, writes = 0;
  const nav = { showTOC: true, showPreviousNext: false, stickyHeader: true, tree: { unlistedPages: "append", items: [{ kind: "page", pageId: "guide" }] } };
  const env = environment({ settings: { navigation: nav }, respond({ route, request, search, body }) {
    if (route === "settings" && request.method === "PUT") { if (++writes === 1) { published = true; return json({ error: { code: "conflict", message: "Navigation changed elsewhere" } }, 409); } return json({ ...body, revision: "s3" }); }
    if (published && route === "settings" && request.method === "GET") return json({ revision: "s2", title: "Docs", theme: {}, navigation: nav });
    if (published && route === "documents" && search === "?published=true") return json({ revision: "s2", documents: [{ ...source, content: undefined }] });
  } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("keep conflict draft"); await env.app.navigate("navigation");
  const form = env.document.querySelector(".navigation-form"), label = form.querySelector('[name="navigationPageTitle"]'); label.value = "My retained label"; env.event(label, "input"); env.event(form, "submit"); await settle();
  assert.equal(env.document.querySelector(".navigation-form"), form); assert.equal(label.value, "My retained label"); assert.match(env.document.querySelector('#view [role="alert"]').textContent, /retained/);
  env.event(controls(env.document, "Review latest navigation")[0], "click"); await settle(); assert.equal(env.document.querySelector(".navigation-form"), form); assert.equal(label.value, "My retained label");
  env.event(controls(env.document, "Use reviewed navigation revision")[0], "click"); env.event(form, "submit"); await settle();
  const request = env.requests.filter(item => item.route === "settings" && item.method === "PUT")[1]; assert.equal(request.body.revision, "s2"); assert.equal(request.body.navigation.tree.items[0].title, "My retained label"); assert.equal(env.app.workspace.content, "keep conflict draft"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("late navigation loads and saves cannot replace the selected view or clear another form", async () => {
  let block = false, finishRead;
  const pending = new Promise(resolve => { finishRead = resolve; });
  const env = environment({ respond({ route, request }) { if (block && route === "settings" && request.method === "GET") return pending; } });
  await env.app.ready; await env.app.openDocument("guide"); block = true; const navigation = env.app.navigate("navigation"); await settle(); await env.app.navigate("documents");
  const editor = env.document.querySelector(".source-editor"); finishRead(json({ revision: "s1", navigation: { showTOC: true, showPreviousNext: true, stickyHeader: true } })); await navigation;
  assert.equal(env.document.querySelector(".source-editor"), editor); assert.equal(env.document.querySelector(".navigation-form"), null); env.app.dispose();

  let finishSave, submitted;
  const saving = new Promise(resolve => { finishSave = resolve; });
  const other = environment({ respond({ route, request, body }) { if (route === "settings" && request.method === "PUT") { submitted = body; return saving; } } });
  await other.app.ready; await other.app.openDocument("guide"); other.app.workspace.edit("still my source"); await other.app.navigate("navigation"); other.event(controls(other.document, "Customize navigation")[0], "click"); other.event(other.document.querySelector(".navigation-form"), "submit"); await settle();
  await other.app.navigate("settings"); const form = other.document.querySelector(".settings-form"); form.querySelector('[name="title"]').value = "New unsaved settings"; other.event(form, "input"); other.window.confirm = () => false;
  finishSave(json({ ...submitted, revision: "late" })); await settle(); await other.app.navigate("documents");
  assert.equal(other.document.querySelector(".settings-form"), form); assert.equal(form.querySelector('[name="title"]').value, "New unsaved settings"); assert.equal(other.app.workspace.content, "still my source"); assert.doesNotMatch(other.document.getElementById("message").textContent, /Navigation saved/); other.app.dispose();
});

test("published page picker and section labels remain text while adding an unplaced page", async () => {
  const tree = { unlistedPages: "omit", items: [{ kind: "group", id: "guides", title: "<img src=x onerror=attack()>", children: [{ kind: "page", pageId: "guide" }] }] };
  const env = environment({ settings: { navigation: { showTOC: true, showPreviousNext: true, stickyHeader: true, tree } } }); await env.app.ready; await env.app.navigate("navigation");
  assert.equal(env.document.querySelector(".navigation-form img"), null);
  const parent = env.document.querySelector('[name="navigationParent"]'); parent.value = "[0]";
  assert.equal(env.document.querySelector('[name="navigationPage"]').value, "reference"); env.event(controls(env.document, "Add selected page")[0], "click");
  assert.equal(env.document.querySelectorAll('[data-navigation-group="guides"] [data-navigation-page]').length, 2);
  env.event(env.document.querySelector(".navigation-form"), "submit"); await settle(); const request = env.requests.find(item => item.route === "settings" && item.method === "PUT");
  assert.equal(request.body.navigation.tree.items[0].title, "<img src=x onerror=attack()>"); assert.equal(request.body.navigation.tree.items[0].children[1].pageId, "reference"); assert.equal(Object.hasOwn(request.body, "sync"), false); env.app.dispose();
});

test("folder deletion reviews the full plan and always stages before an explicit publish", async () => {
  let draft = null;
  const env = environment({ settings: { publishing: { autoPublish: true, gitPushOnPublish: false } }, files: [{ path: "docs", name: "docs", directory: true, revision: "p1", canWrite: true }], respond({ route }) {
    if (route === "files/delete-preview") return json(deletionPlan());
    if (route === "files/delete-tree") { draft = { id: "delete-draft", revision: "delete-cas", baseRevision: "p1", path: "docs", operation: "delete-tree" }; return json({ revision: "p1", published: false, draft }); }
    if (route === "drafts") return json({ drafts: draft ? [draft] : [] });
    if (route === "drafts/delete-draft/publish") return json({ revision: "p2", published: true });
  } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("unsaved document remains"); const document = env.app.workspace.document;
  await env.app.navigate("files"); env.event(controls(env.document, "Review folder deletion")[0], "click"); await settle();
  assert.match(env.document.querySelector('[aria-label="Folder deletion review"]').textContent, /2 file\(s\), 1 folder\(s\), 1 document\(s\)/);
  assert.match(env.document.querySelector('[aria-label="Folder deletion review"]').textContent, /open source.*docs\/guide.md.*unsaved/s);
  assert.equal(env.requests.some(item => item.route === "files/delete-tree"), false);
  env.event(controls(env.document, "Stage folder deletion")[0], "click"); await settle();
  const staged = env.requests.find(item => item.route === "files/delete-tree"); assert.deepEqual(staged.body, { path: "docs", revision: "p1", planRevision: "plan-1" });
  assert.equal(staged.headers["X-CSRF-Token"], "csrf-memory"); assert.equal(env.requests.some(item => item.route.endsWith("/publish")), false);
  assert.equal(env.document.getElementById("view-title").textContent, "Publishing"); assert.match(env.document.getElementById("message").textContent, /Published files are unchanged/);
  assert.equal(env.app.workspace.document, document); assert.equal(env.app.workspace.content, "unsaved document remains"); assert.equal(env.app.workspace.dirty, true);
  env.window.confirm = () => false; env.event(controls(env.document, "Publish")[0], "click"); await settle(); assert.equal(env.requests.some(item => item.route === "drafts/delete-draft/publish"), false);
  env.window.confirm = () => true; env.event(controls(env.document, "Publish")[0], "click"); await settle(); assert.equal(env.requests.filter(item => item.route === "drafts/delete-draft/publish").length, 1);
  assert.equal(env.app.workspace.content, "unsaved document remains"); assert.equal(env.app.workspace.document, document); env.app.dispose();
});

test("truncated folder plans explain their scope and cancellation or stale CAS never stages again", async () => {
  const env = environment({ confirm: false, files: [{ path: "docs", name: "docs", directory: true, revision: "p1", canWrite: true }], respond({ route }) {
    if (route === "files/delete-preview") return json(deletionPlan(128));
    if (route === "files/delete-tree") return json({ error: { code: "conflict", message: "The folder changed. Review it again." } }, 409);
  } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("keep this text"); await env.app.navigate("files"); env.event(controls(env.document, "Review folder deletion")[0], "click"); await settle();
  assert.match(env.document.querySelector('[aria-label="Folder deletion review"]').textContent, /first 100 of 128/);
  const stage = controls(env.document, "Stage folder deletion")[0]; env.event(stage, "click"); await settle(); assert.equal(env.requests.some(item => item.route === "files/delete-tree"), false);
  env.window.confirm = () => true; env.event(stage, "click"); await settle(); assert.equal(env.requests.filter(item => item.route === "files/delete-tree").length, 1);
  assert.equal(stage.disabled, true); assert.match(env.document.querySelector('#view [role="alert"]').textContent, /retained/);
  assert.equal(env.requests.filter(item => item.route === "files/delete-preview").length, 1); assert.equal(env.app.workspace.content, "keep this text"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("partial folder access hides recursive deletion and failed authorization reveals no inventory", async () => {
  const files = [{ path: "docs", name: "docs", directory: true, revision: "p1", canWrite: true }];
  const partial = environment({ files, user: { role: "editor", grants: { read: ["docs/public"], write: ["."] } } }); await partial.app.ready; await partial.app.navigate("files");
  assert.equal(controls(partial.document, "Review folder deletion").length, 0); assert.equal(partial.requests.some(item => item.route === "files/delete-preview"), false); partial.app.dispose();
  const env = environment({ files, respond({ route }) { if (route === "files/delete-preview") return json({ error: { code: "forbidden", message: "The folder is unavailable.", entries: [{ path: "private/secret.go" }], fileCount: 932 } }, 403); } });
  await env.app.ready; await env.app.navigate("files"); env.event(controls(env.document, "Review folder deletion")[0], "click"); await settle();
  assert.equal(controls(env.document, "Stage folder deletion").length, 0); assert.doesNotMatch(env.document.getElementById("view").textContent, /private\/secret|932/); env.app.dispose();
});

test("canceling a pending plan and leaving during staging cannot replace the current editor", async () => {
  let finishPlan; const plan = new Promise(resolve => { finishPlan = resolve; });
  const files = [{ path: "docs", name: "docs", directory: true, revision: "p1", canWrite: true }];
  const env = environment({ files, respond({ route }) { if (route === "files/delete-preview") return plan; } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("retained local draft"); await env.app.navigate("files"); env.event(controls(env.document, "Review folder deletion")[0], "click"); await settle();
  env.event(controls(env.document, "Cancel deletion review")[0], "click"); finishPlan(json(deletionPlan())); await settle(); assert.equal(controls(env.document, "Stage folder deletion").length, 0); assert.equal(env.app.workspace.content, "retained local draft"); env.app.dispose();
  let finishStage; const pending = new Promise(resolve => { finishStage = resolve; });
  const other = environment({ files, respond({ route }) { if (route === "files/delete-preview") return json(deletionPlan()); if (route === "files/delete-tree") return pending; } });
  await other.app.ready; await other.app.openDocument("guide"); other.app.workspace.edit("newer editor input"); await other.app.navigate("files"); other.event(controls(other.document, "Review folder deletion")[0], "click"); await settle(); other.event(controls(other.document, "Stage folder deletion")[0], "click"); await settle();
  await other.app.navigate("documents"); const editor = other.document.querySelector(".source-editor"); editor.value = "even newer local input"; other.event(editor, "input"); finishStage(json({ revision: "p1", published: false, draft: { id: "d1", revision: "d-cas" } })); await settle();
  assert.equal(other.document.querySelector(".source-editor"), editor); assert.equal(other.app.workspace.content, "even newer local input"); assert.notEqual(other.document.getElementById("view-title").textContent, "Publishing"); other.app.dispose();
});

for (const extension of ["md", "json", "html"]) test("unregistered ." + extension + " source and owner drafts reopen as literal text from Files and Publishing", async () => {
  const path = "snippet." + extension;
  let saved = "<script id=snippet-injection>neverRun()</script>\r\n", draft = { id: "snippet-draft", revision: "d1", baseRevision: "p1", path, operation: "write" }, language;
  const env = environment({ files: [{ path, name: path, directory: false, size: 40, revision: "p1", canWrite: true }], settings: { editor: { defaultFormat: "markdown", previewOnSave: true } }, loadEditor: async () => ({ createEditor(_host, options) { language = options.language; throw new Error("Keep fallback"); } }), respond({ route, request, search, body }) {
    if (route === "drafts") return json({ drafts: [draft] });
    if (route === "files/content" && request.method === "GET") {
      const draftId = new URLSearchParams(search).get("draftId");
      if (draftId) assert.equal(draftId, draft.id);
      return new Response(draftId ? saved : "published snippet\r\n", { headers: { ETag: '"' + (draftId ? draft.revision : "p1") + '"', ...(draftId ? { "X-Base-Revision": draft.baseRevision } : {}) } });
    }
    if (route === "files/content" && request.method === "PUT") { saved = body; draft = { ...draft, revision: "d2" }; return json({ revision: "p1", published: false, draft }); }
  } });
  await env.app.ready; await env.app.navigate("files");
  assert.equal(controls(env.document, "Open source").length, 1); assert.equal(controls(env.document, "Open saved draft").length, 1);
  env.event(controls(env.document, "Open source")[0], "click"); await settle();
  assert.equal(env.app.workspace.document.resource, "file"); assert.equal(env.app.workspace.document.kind, undefined); assert.equal(language, "text"); assert.equal(controls(env.document, "Preview").length, 0);
  const text = env.document.querySelector(".source-editor"); text.value = saved + "edited\r\n"; env.event(text, "input"); env.event(controls(env.document, "Save draft")[0], "click"); await settle();
  const put = env.requests.find(item => item.route === "files/content" && item.method === "PUT"); assert.equal(put.body, saved); assert.equal(put.headers["If-Match"], '"p1"'); assert.equal(env.app.workspace.document.draftRevision, "d2");
  await env.app.navigate("files"); env.event(controls(env.document, "Open saved draft")[0], "click"); await settle(); assert.equal(env.app.workspace.content, saved); assert.equal(env.app.workspace.document.draftId, draft.id); assert.equal(env.app.workspace.document.draftRevision, "d2");
  await env.app.navigate("publishing"); env.event(controls(env.document, "Open source draft")[0], "click"); await settle(); assert.equal(env.app.workspace.content, saved); assert.equal(env.app.workspace.document.baseRevision, "p1");
  assert.equal(env.document.querySelector("#snippet-injection"), null); assert.equal(controls(env.document, "Preview").length, 0); assert.equal(env.requests.some(item => item.route === "preview"), false); assert.equal(env.requests.some(item => item.route.startsWith("documents/")), false); env.app.dispose();
});

test("registered source routes to the document editor and retains the selected owner draft CAS", async () => {
  const registered = { ...source, path: "guide.md" };
  let draft = { id: "raw-registered", revision: "d7", baseRevision: "p0", path: registered.path, operation: "write" }, content = "# Raw uploaded draft\r\n";
  const env = environment({ files: [{ path: registered.path, name: "guide.md", directory: false, size: 20, revision: "p1", canWrite: true }], respond({ route, request, body }) {
    if (route === "documents" && request.method === "GET") return json({ documents: [{ ...registered, content: undefined }], revision: "p1" });
    if (route === "drafts") return json({ drafts: [draft] });
    if (route === "documents/guide") {
      if (request.method === "PUT") { assert.equal(body.draftId, draft.id); assert.equal(body.draftRevision, draft.revision); assert.equal(body.baseRevision, "p0"); assert.equal(body.revision, "p0"); content = body.content; draft = { ...draft, revision: "d8" }; return json({ revision: "p0", published: false, draft, document: { ...registered, revision: "p0", content, draft: true, draftId: draft.id, draftRevision: draft.revision, baseRevision: "p0" } }); }
      return json({ ...registered, revision: "p0", content, draft: true, draftId: draft.id, draftRevision: draft.revision, baseRevision: "p0" });
    }
  } });
  await env.app.ready; await env.app.navigate("files"); env.event(controls(env.document, "Open source")[0], "click"); await settle();
  assert.equal(env.app.workspace.document.id, "guide"); assert.equal(env.app.workspace.document.resource, undefined); assert.equal(controls(env.document, "Preview").length, 1); assert.equal(controls(env.document, "History").length, 1);
  await env.app.navigate("files"); env.event(controls(env.document, "Open saved draft")[0], "click"); await settle(); assert.equal(env.app.workspace.document.draftId, "raw-registered"); assert.equal(env.app.workspace.document.draftRevision, "d7");
  const text = env.document.querySelector(".source-editor"); text.value += "kept in same draft\r\n"; env.event(text, "input"); env.event(controls(env.document, "Save draft")[0], "click"); await settle(); assert.equal(env.app.workspace.document.draftRevision, "d8");
  await env.app.navigate("publishing"); env.event(controls(env.document, "Open source draft")[0], "click"); await settle(); assert.equal(env.app.workspace.document.id, "guide"); assert.equal(env.app.workspace.content, content); assert.equal(env.app.workspace.document.baseRevision, "p0"); assert.equal(env.requests.some(item => item.route === "files/content"), false); env.app.dispose();
});

test("registered source redirect preserves unsaved literal text on cancellation and newer selected draft conflicts", async () => {
  const registered = { ...source, path: "guide.md" };
  const draft = { id: "raw-registered", revision: "d7", baseRevision: "p0", path: registered.path, operation: "write" };
  const env = environment({ confirm: false, drafts: [draft], files: [{ path: registered.path, name: "guide.md", directory: false, size: 20, revision: "p1", canWrite: true }], respond({ route, request }) {
    if (route === "documents" && request.method === "GET") return json({ documents: [{ ...registered, content: undefined }], revision: "p1" });
    if (route === "files/content") return new Response("unregistered original", { headers: { ETag: '"p1"' } });
    if (route === "documents/guide") return json({ ...registered, content: "newer registered draft", draft: true, draftId: draft.id, draftRevision: "d8", baseRevision: "p0" });
  } });
  await env.app.ready; await env.app.openSource("snippet.json"); env.app.workspace.edit("keep my unsaved literal text"); const previous = env.app.workspace.document;
  await env.app.navigate("files"); env.event(controls(env.document, "Open source")[0], "click"); await settle(); assert.equal(env.app.workspace.document, previous); assert.equal(env.app.workspace.content, "keep my unsaved literal text"); assert.equal(env.requests.some(item => item.route === "documents/guide"), false);
  env.window.confirm = () => true; env.event(controls(env.document, "Open saved draft")[0], "click"); await settle(); assert.match(env.document.getElementById("error").textContent, /saved document draft changed/i); assert.equal(env.app.workspace.document, previous); assert.equal(env.app.workspace.content, "keep my unsaved literal text"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("late registered draft responses cannot replace a more recently opened literal source", async () => {
  let finish; const pending = new Promise(resolve => { finish = resolve; });
  const draft = { id: "raw-registered", revision: "d7", baseRevision: "p0", path: source.path, operation: "write" };
  const env = environment({ respond({ route }) { if (route === "documents/guide") return pending; if (route === "files/content") return new Response("latest literal source", { headers: { ETag: '"p1"' } }); } });
  await env.app.ready; const old = env.app.openSource(source.path, draft); await settle(); await env.app.openSource("snippet.html");
  finish(json({ ...source, content: "obsolete registered response", draft: true, draftId: draft.id, draftRevision: draft.revision, baseRevision: draft.baseRevision })); await old;
  assert.equal(env.app.workspace.document.path, "snippet.html"); assert.equal(env.app.workspace.document.resource, "file"); assert.equal(env.app.workspace.content, "latest literal source"); assert.equal(controls(env.document, "Preview").length, 0); env.app.dispose();
});

test("literal code source uses the existing text editor without preview and resaves its exact owner draft", async () => {
  let content = "\uFEFF// пример\r\n<script>neverRun()</script>\r\n", draft = null, language = null, saves = 0;
  const file = { path: "example.js", name: "example.js", directory: false, size: 40, revision: "p1", canWrite: true };
  const env = environment({ files: [file], settings: { editor: { defaultFormat: "markdown", previewOnSave: true } }, loadEditor: async () => ({ createEditor(_host, options) { language = options.language; throw new Error("Keep fallback"); } }), respond({ route, request, search, body }) {
    if (route === "drafts") return json({ drafts: draft ? [draft] : [] });
    if (route === "files/content" && request.method === "GET") return new Response(content, { headers: { "Content-Type": "application/octet-stream", ETag: '"' + (new URLSearchParams(search).has("draftId") ? draft.revision : "p1") + '"', ...(new URLSearchParams(search).has("draftId") ? { "X-Base-Revision": "p1" } : {}) } });
    if (route === "files/content" && request.method === "PUT") { content = body; draft = { id: "source-draft", revision: "d" + (++saves), baseRevision: "p1", path: file.path, operation: "write" }; return json({ revision: "p1", published: false, draft }); }
  } });
  await env.app.ready; await env.app.navigate("files"); env.event(controls(env.document, "Open source")[0], "click"); await settle();
  assert.equal(language, "text"); assert.equal(env.app.workspace.document.resource, "file"); assert.equal(env.document.querySelector(".source-editor").value, content); assert.equal(env.document.querySelector(".editor-host script"), null);
  assert.equal(controls(env.document, "Preview").length, 0); assert.equal(controls(env.document, "History").length, 1);
  const text = env.document.querySelector(".source-editor"); text.value += "// edited\r\n"; env.event(text, "input"); env.event(controls(env.document, "Save draft")[0], "click"); await settle();
  let put = env.requests.find(item => item.route === "files/content" && item.method === "PUT"); assert.equal(put.body, content); assert.equal(put.headers["If-Match"], '\"p1\"'); assert.equal(put.headers["Content-Type"], "application/octet-stream"); assert.equal(env.requests.some(item => item.route === "preview"), false);
  await env.app.navigate("files"); env.event(controls(env.document, "Open saved draft")[0], "click"); await settle(); assert.equal(env.app.workspace.document.draftRevision, "d1");
  const next = env.document.querySelector(".source-editor"); next.value += "// second\r\n"; env.event(next, "input"); env.event(controls(env.document, "Save draft")[0], "click"); await settle();
  put = env.requests.filter(item => item.route === "files/content" && item.method === "PUT")[1]; assert.equal(put.headers["X-Draft-ID"], "source-draft"); assert.equal(put.headers["X-Draft-Revision"], "d1"); assert.equal(put.headers["If-Match"], '\"p1\"'); assert.equal(env.app.workspace.document.draftRevision, "d2"); env.app.dispose();
});

test("opening source never silently replaces an unsaved document and viewers remain readonly", async () => {
  const files = [{ path: "sample.go", name: "sample.go", directory: false, size: 8, revision: "p1", canWrite: true }];
  const env = environment({ files, confirm: false }); await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("keep registered doc"); const document = env.app.workspace.document;
  await env.app.navigate("files"); env.event(controls(env.document, "Open source")[0], "click"); await settle(); assert.equal(env.app.workspace.document, document); assert.equal(env.app.workspace.content, "keep registered doc"); assert.equal(env.requests.some(item => item.route === "files/content"), false); env.app.dispose();
  const viewer = environment({ files, user: { role: "viewer" }, respond({ route }) { if (route === "files/content") return new Response("package main\n", { headers: { ETag: '\"p1\"' } }); } });
  await viewer.app.ready; await viewer.app.navigate("files"); viewer.event(controls(viewer.document, "Open source")[0], "click"); await settle(); assert.equal(viewer.document.querySelector(".source-editor").readOnly, true); assert.equal(controls(viewer.document, "Save draft").length, 0); assert.equal(controls(viewer.document, "Publish").length, 0); viewer.app.dispose();
});

test("draft-only literal files reopen from Files and Publishing with safe draft download links", async () => {
  const draft = { id: "sql-draft", revision: "d4", baseRevision: "p0", path: "new.sql", operation: "write" };
  const env = environment({ files: [], drafts: [draft], respond({ route, search }) { if (route === "files/content") { assert.equal(new URLSearchParams(search).get("draftId"), draft.id); return new Response("SELECT '<script>';\r\n", { headers: { ETag: '\"d4\"', "X-Base-Revision": "p0" } }); } } });
  await env.app.ready; await env.app.navigate("files"); assert.match(env.document.getElementById("view").textContent, /Saved source draft/); assert.equal(controls(env.document, "Move").length, 0); assert.equal(controls(env.document, "Delete").length, 0);
  const download = [...env.document.querySelectorAll("a")].find(node => node.textContent === "Download draft"); assert.equal(download.getAttribute("href"), "https://docs.test/team/_docs/api/v1/files/content?path=new.sql&draftId=sql-draft");
  env.event(controls(env.document, "Open saved draft")[0], "click"); await settle(); assert.equal(env.app.workspace.document.revision, "p0"); assert.equal(env.app.workspace.document.draftRevision, "d4");
  await env.app.navigate("publishing"); env.event(controls(env.document, "Open source draft")[0], "click"); await settle(); assert.equal(env.app.workspace.content, "SELECT '<script>';\r\n"); assert.equal(env.document.querySelector(".editor-host script"), null); env.app.dispose();
});

test("source CAS conflicts keep text and original base until an explicitly confirmed reviewed rebase", async () => {
  const draft = { id: "saved-go", revision: "d1", baseRevision: "p0", path: "main.go", operation: "write" };
  const env = environment({ files: [], drafts: [draft], respond({ route, request, search }) {
    if (route !== "files/content") return;
    if (request.method === "PUT") return json({ error: { code: "conflict", message: "A newer saved source draft exists", currentRevision: "p2" } }, 409);
    const saved = new URLSearchParams(search).has("draftId"); return new Response(saved ? "old saved source" : "current published source", { headers: { ETag: saved ? '\"d1\"' : '\"p2\"', ...(saved ? { "X-Base-Revision": "p0" } : {}) } });
  } });
  await env.app.ready; await env.app.navigate("files"); env.event(controls(env.document, "Open saved draft")[0], "click"); await settle(); const text = env.document.querySelector(".source-editor"); text.value = "my retained source"; env.event(text, "input"); env.event(controls(env.document, "Save draft")[0], "click"); await settle();
  assert.equal(env.app.workspace.content, "my retained source"); assert.equal(env.app.workspace.document.baseRevision, "p0"); assert.equal(env.app.workspace.document.draftRevision, "d1"); assert.equal(env.app.workspace.dirty, true);
  env.event(controls(env.document, "Compare latest")[0], "click"); await settle(); assert.equal(env.document.querySelector('[aria-label="Latest published source"]').value, "current published source"); assert.equal(controls(env.document, "Save merged draft")[0].hidden, false); assert.equal(env.document.querySelector('[aria-label="Latest published source"]').readOnly, true);
  assert.equal(env.requests.some(item => item.route === "files/rebase"), false); assert.equal(env.app.workspace.document.baseRevision, "p0"); env.window.confirm = () => false; env.event(controls(env.document, "Save merged draft")[0], "click"); await settle(); assert.equal(env.requests.some(item => item.route === "files/rebase"), false); assert.equal(env.app.workspace.document.draftRevision, "d1"); env.event(controls(env.document, "Open current published source")[0], "click"); await settle(); assert.equal(env.app.workspace.content, "my retained source"); env.app.dispose();
});

test("Create text file opens an empty unsaved literal buffer without a request until Save", async () => {
  const env = environment({ files: [], respond({ route, request, search, body }) {
    if (route === "files/content" && request.method === "PUT") { assert.equal(new URLSearchParams(search).get("path"), "empty.txt"); assert.equal(body, ""); return json({ revision: "p1", published: false, draft: { id: "empty-source", revision: "d1", baseRevision: "p1", path: "empty.txt", operation: "write" } }); }
  } });
  await env.app.ready; await env.app.navigate("files"); const requestsBefore = env.requests.length;
  env.event(controls(env.document, "Create text file")[0], "click"); env.document.querySelector('#dialog-fields [name="name"]').value = "empty.txt"; env.event(env.document.getElementById("dialog-form"), "submit"); await settle();
  assert.equal(env.requests.length, requestsBefore); assert.equal(env.app.workspace.document.path, "empty.txt"); assert.equal(env.app.workspace.document.resource, "file"); assert.equal(env.app.workspace.document.isNew, true); assert.equal(env.app.workspace.dirty, true); assert.equal(env.document.querySelector(".source-editor").value, ""); assert.equal(controls(env.document, "Preview").length, 0); assert.equal(controls(env.document, "Save draft")[0].disabled, false);
  env.event(controls(env.document, "Save draft")[0], "click"); await settle(); const saves = env.requests.filter(item => item.route === "files/content"); assert.equal(saves.length, 1); assert.equal(saves[0].method, "PUT"); assert.equal(saves[0].headers["If-Match"], '"p1"'); assert.equal(env.app.workspace.document.isNew, false); assert.equal(env.app.workspace.document.draftId, "empty-source"); assert.equal(env.app.workspace.dirty, false); env.app.dispose();
});

test("Create text file refuses published and draft aliases and retains a dirty editor when discard is canceled", async () => {
  const env = environment({ confirm: false, files: [{ path: "Existing.TXT", name: "Existing.TXT", directory: false, revision: "p1", canWrite: true }], drafts: [{ id: "existing-draft", revision: "d1", baseRevision: "p1", path: "saved.go", operation: "write" }] });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("keep unsaved document"); const previous = env.app.workspace.document; await env.app.navigate("files");
  env.event(controls(env.document, "Create text file")[0], "click"); const name = env.document.querySelector('#dialog-fields [name="name"]');
  name.value = "existing.txt"; env.event(env.document.getElementById("dialog-form"), "submit"); await settle(); assert.match(env.document.getElementById("dialog-error").textContent, /already uses this name/i);
  name.value = "saved.go"; env.event(env.document.getElementById("dialog-form"), "submit"); await settle(); assert.match(env.document.getElementById("dialog-error").textContent, /saved change/i);
  name.value = "new.html"; env.event(env.document.getElementById("dialog-form"), "submit"); await settle(); assert.equal(env.app.workspace.document, previous); assert.equal(env.app.workspace.content, "keep unsaved document"); assert.equal(env.app.workspace.dirty, true); assert.equal(env.document.getElementById("action-dialog").open, true); assert.equal(env.requests.some(item => item.route === "files/content"), false); env.app.dispose();
});

test("reviewed raw rebase remains a separate draft with autoPublish and requires reviewing again after a conflict", async () => {
  let current = "p2", attempts = 0;
  const env = environment({ settings: { publishing: { autoPublish: true, gitPushOnPublish: true } }, respond({ route, request, body }) {
    if (route === "files/content") return request.method === "PUT" ? json({ error: { code: "conflict", message: "The source changed", currentRevision: current } }, 409) : new Response("current published " + current + "\r\n", { headers: { ETag: '"' + current + '"' } });
    if (route === "files/rebase") { attempts++; if (attempts === 1) { current = "p3"; return json({ error: { code: "conflict", message: "The reviewed version changed", currentRevision: current } }, 409); } assert.equal(body, "\uFEFFmy reviewed merge\r\n"); return json({ revision: current, published: false, draft: { id: "new-merged", revision: "d2", baseRevision: current, path: "example.go", operation: "write" } }); }
  } });
  await env.app.ready; await env.app.openSource("example.go"); const text = env.document.querySelector(".source-editor"); text.value = "\uFEFFmy reviewed merge\r\n"; env.event(text, "input"); env.event(controls(env.document, "Save and publish")[0], "click"); await settle();
  assert.equal(controls(env.document, "Save merged draft")[0].hidden, true); env.event(controls(env.document, "Compare latest")[0], "click"); await settle(); assert.equal(env.document.querySelector('[aria-label="Latest published source"]').readOnly, true); assert.equal(env.document.querySelector('[aria-label="Latest published source"]').value, "current published p2\r\n");
  env.event(controls(env.document, "Save merged draft")[0], "click"); await settle(); assert.equal(attempts, 1); assert.equal(env.app.workspace.content, "\uFEFFmy reviewed merge\r\n"); assert.equal(env.app.workspace.document.draftId, ""); assert.equal(env.app.workspace.dirty, true); assert.equal(controls(env.document, "Save merged draft")[0].hidden, true);
  env.event(controls(env.document, "Save merged draft")[0], "click"); await settle(); assert.equal(attempts, 1); env.event(controls(env.document, "Compare latest")[0], "click"); await settle(); env.event(controls(env.document, "Save merged draft")[0], "click"); await settle();
  const rebases = env.requests.filter(item => item.route === "files/rebase"); assert.equal(rebases.length, 2); assert.deepEqual(rebases.map(item => item.headers["If-Match"]), ['"p2"', '"p3"']); assert.equal(rebases.every(item => item.method === "POST" && item.headers["Content-Type"] === "application/octet-stream" && !item.headers["X-Draft-ID"] && !item.headers["X-Draft-Revision"]), true);
  assert.equal(env.requests.some(item => /drafts\/.*\/publish/.test(item.route) || item.route === "sync"), false); assert.equal(env.requests.filter(item => item.route === "files/content" && item.method === "PUT").length, 1); assert.equal(env.app.workspace.document.draftId, "new-merged"); assert.equal(env.app.workspace.document.baseRevision, "p3"); assert.equal(env.app.workspace.dirty, false); assert.match(env.document.getElementById("message").textContent, /saved as a draft/i); env.app.dispose();
});

test("a compare action from an older resource cannot rebase the more recently selected file", async () => {
  const env = environment({ respond({ route, request, search }) {
    if (route !== "files/content") return;
    if (request.method === "PUT") return json({ error: { code: "conflict", message: "Source changed", currentRevision: "p2" } }, 409);
    const path = new URLSearchParams(search).get("path"); return new Response(path === "new.json" ? "new resource" : "published", { headers: { ETag: '"p2"' } });
  } });
  await env.app.ready; await env.app.openSource("old.md"); env.app.workspace.edit("old merge"); env.event(controls(env.document, "Save draft")[0], "click"); await settle(); env.event(controls(env.document, "Compare latest")[0], "click"); await settle(); const oldAction = controls(env.document, "Save merged draft")[0];
  await env.app.openSource("new.json"); env.event(oldAction, "click"); await settle(); assert.equal(env.requests.some(item => item.route === "files/rebase"), false); assert.equal(env.app.workspace.document.path, "new.json"); assert.equal(env.app.workspace.content, "new resource"); assert.match(env.document.getElementById("error").textContent, /Compare the latest source again/); env.app.dispose();
});

test("exact-path history reaches deleted files and restores zero bytes without replacing a dirty editor or registering a page", async () => {
  const path = "deleted.md";
  const env = environment({ files: [], respond({ route, request, search, body }) {
    if (route === "files/history") { assert.equal(new URLSearchParams(search).get("path"), path); return json({ path, revision: "p2", revisions: [historical(path, { id: "removed", deleted: true, restorable: false }), historical(path, { author: "Author <img id=history-injection src=x>" })] }); }
    if (route === "files/restore") { assert.deepEqual(body, { path, historyId: "h1", revision: "p2" }); return json({ revision: "p2", published: false, draft: { id: "restored-raw", revision: "dr2", baseRevision: "p2", path, operation: "write" } }); }
    if (route === "files/content" && request.method === "GET") { const draftId = new URLSearchParams(search).get("draftId"); return new Response(draftId ? "" : "\uFEFFactive source\r\n", { headers: { ETag: draftId ? '"dr2"' : '"p1"', ...(draftId ? { "X-Base-Revision": "p2" } : {}) } }); }
  } });
  await env.app.ready; await env.app.openSource("active.sql"); env.app.workspace.edit("\uFEFFkeep active changes\r\n"); const previous = env.app.workspace.document; await env.app.navigate("files"); env.event(controls(env.document, "File history")[0], "click"); env.document.querySelector('#dialog-fields [name="path"]').value = path; env.event(env.document.getElementById("dialog-form"), "submit"); await settle();
  assert.match(env.document.getElementById("view").textContent, /Deleted/); assert.match(env.document.getElementById("view").textContent, /does not recreate a deleted document entry/i); assert.equal(controls(env.document, "Restore as draft").length, 1); assert.equal(env.document.querySelector("#history-injection"), null);
  env.event(controls(env.document, "Restore as draft")[0], "click"); await settle(); assert.equal(env.app.workspace.document, previous); assert.equal(env.app.workspace.content, "\uFEFFkeep active changes\r\n"); assert.equal(env.app.workspace.dirty, true); assert.equal(env.requests.some(item => item.route === "documents" && item.method === "POST" || /drafts\/.*\/publish/.test(item.route)), false);
  const reads = env.requests.filter(item => item.route === "files/content").length; env.window.confirm = () => false; env.event(controls(env.document, "Open restored draft")[0], "click"); await settle(); assert.equal(env.app.workspace.document, previous); assert.equal(env.requests.filter(item => item.route === "files/content").length, reads);
  env.window.confirm = () => true; env.event(controls(env.document, "Open restored draft")[0], "click"); await settle(); assert.equal(env.app.workspace.document.path, path); assert.equal(env.app.workspace.document.resource, "file"); assert.equal(env.app.workspace.document.draftId, "restored-raw"); assert.equal(env.app.workspace.document.baseRevision, "p2"); assert.equal(env.app.workspace.content, ""); assert.equal(controls(env.document, "Preview").length, 0); env.app.dispose();
});

test("read-only file history exposes no restore action and rejects another exact path before requesting it", async () => {
  const path = "allowed.md";
  const env = environment({ files: [], user: { role: "viewer", grants: { read: [path], write: [] } }, respond({ route, search }) { if (route === "files/history") { assert.equal(new URLSearchParams(search).get("path"), path); return json({ path, revision: "p2", revisions: [historical(path)] }); } } });
  await env.app.ready; await env.app.navigate("files"); assert.equal(controls(env.document, "Create text file").length, 0); env.event(controls(env.document, "File history")[0], "click"); env.document.querySelector('#dialog-fields [name="path"]').value = path; env.event(env.document.getElementById("dialog-form"), "submit"); await settle(); assert.equal(controls(env.document, "Restore as draft").length, 0);
  env.event(controls(env.document, "Choose file path")[0], "click"); env.document.querySelector('#dialog-fields [name="path"]').value = "private.md"; env.event(env.document.getElementById("dialog-form"), "submit"); await settle(); assert.match(env.document.getElementById("dialog-error").textContent, /cannot read history/i); assert.equal(env.requests.filter(item => item.route === "files/history").length, 1); assert.equal(env.requests.some(item => item.route === "files/restore"), false); env.app.dispose();
});

test("a stale restore requires an explicit history refresh before another reviewed attempt", async () => {
  const path = "sample.go"; let histories = 0, restores = 0;
  const env = environment({ files: [{ path, name: path, directory: false, revision: "p1", canWrite: true }], respond({ route, body }) {
    if (route === "files/history") return json({ path, revision: "p" + (++histories + 1), revisions: [historical(path)] });
    if (route === "files/restore") { restores++; if (restores === 1) return json({ error: { code: "conflict", message: "Project changed", currentRevision: "p3" } }, 409); assert.equal(body.revision, "p3"); return json({ revision: "p3", published: false, draft: { id: "restored", revision: "d3", baseRevision: "p3", path, operation: "write" } }); }
  } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("keep editor"); const previous = env.app.workspace.document; await env.app.navigate("files"); env.event(controls(env.document, "History")[0], "click"); await settle(); env.event(controls(env.document, "Restore as draft")[0], "click"); await settle(); assert.equal(restores, 1); assert.equal(histories, 1); assert.equal(controls(env.document, "Restore as draft")[0].disabled, true); assert.equal(env.app.workspace.document, previous); assert.equal(env.app.workspace.content, "keep editor");
  env.event(controls(env.document, "Refresh history")[0], "click"); await settle(); assert.equal(histories, 2); assert.equal(controls(env.document, "Restore as draft")[0].disabled, false); env.event(controls(env.document, "Restore as draft")[0], "click"); await settle(); assert.equal(restores, 2); assert.equal(env.app.workspace.content, "keep editor"); assert.equal(env.app.workspace.dirty, true); env.app.dispose();
});

test("late history and restore responses never replace the selected editor or newer text", async () => {
  const path = "slow.go"; let finishHistory, finishRestore;
  const historyPending = new Promise(resolve => { finishHistory = resolve; }), restorePending = new Promise(resolve => { finishRestore = resolve; });
  let slow = true;
  const env = environment({ files: [{ path, name: path, directory: false, revision: "p1", canWrite: true }], respond({ route }) { if (route === "files/history") return slow ? historyPending : json({ path, revision: "p2", revisions: [historical(path)] }); if (route === "files/restore") return restorePending; } });
  await env.app.ready; await env.app.openDocument("guide"); env.app.workspace.edit("retained editor"); await env.app.navigate("files"); env.event(controls(env.document, "History")[0], "click"); await settle(); await env.app.navigate("documents"); const first = env.document.querySelector(".source-editor"); first.value = "newer during history"; env.event(first, "input"); finishHistory(json({ path, revision: "p2", revisions: [historical(path)] })); await settle(); assert.equal(env.document.querySelector(".source-editor"), first); assert.equal(env.app.workspace.content, "newer during history");
  slow = false; await env.app.navigate("files"); env.event(controls(env.document, "History")[0], "click"); await settle(); env.event(controls(env.document, "Restore as draft")[0], "click"); await settle(); await env.app.navigate("documents"); const second = env.document.querySelector(".source-editor"); second.value = "newer during restore"; env.event(second, "input"); finishRestore(json({ revision: "p2", published: false, draft: { id: "late-restore", revision: "d2", baseRevision: "p2", path, operation: "write" } })); await settle(); assert.equal(env.document.querySelector(".source-editor"), second); assert.equal(env.app.workspace.document.id, "guide"); assert.equal(env.app.workspace.content, "newer during restore"); assert.equal(env.app.workspace.dirty, true); assert.equal(controls(env.document, "Open restored draft").length, 0); env.app.dispose();
});

test("literal upload preflight uses the text limit and refuses overlapping saved drafts before any upload", async () => {
  const env = environment(); await env.app.ready; await env.app.navigate("files"); const upload = env.document.querySelector('input[type="file"]');
  assert.ok(upload.getAttribute("accept").split(",").includes(".go")); assert.ok(upload.getAttribute("accept").split(",").includes(".xml"));
  const oversized = new File([new Uint8Array((4 << 20) + 1)], "large.go"); Object.defineProperty(upload, "files", { value: [new File(["ok"], "small.go"), oversized] }); env.event(upload, "change"); await settle(); assert.equal(env.requests.some(item => item.route === "files/content" && item.method === "PUT"), false); assert.match(env.document.getElementById("error").textContent, /4 MiB text/); env.app.dispose();
  const overlap = environment({ drafts: [{ id: "existing", revision: "d2", baseRevision: "p1", path: "sample.ts", operation: "write" }] }); await overlap.app.ready; await overlap.app.navigate("files"); const input = overlap.document.querySelector('input[type="file"]'); Object.defineProperty(input, "files", { value: [new File(["new"], "sample.ts")] }); overlap.event(input, "change"); await settle(); assert.equal(overlap.requests.some(item => item.route === "files/content" && item.method === "PUT"), false); assert.match(overlap.document.getElementById("error").textContent, /saved change already targets/); overlap.app.dispose();
});

test("discarding a saved source draft never clears text edited after its confirmation", async () => {
  let finish; const pending = new Promise(resolve => { finish = resolve; });
  const draft = { id: "discard-go", revision: "d1", baseRevision: "p1", path: "sample.go", operation: "write" };
  const env = environment({ files: [], drafts: [draft], respond({ route }) { if (route === "files/content") return new Response("saved source", { headers: { ETag: '\"d1\"', "X-Base-Revision": "p1" } }); if (route === "drafts/discard-go") return pending; } });
  await env.app.ready; await env.app.navigate("files"); env.event(controls(env.document, "Open saved draft")[0], "click"); await settle(); await env.app.navigate("publishing"); env.event(controls(env.document, "Discard")[0], "click"); await settle(); env.app.workspace.edit("new text entered while discard was pending"); finish(json(null, 204)); await settle();
  assert.equal(env.app.workspace.content, "new text entered while discard was pending"); assert.equal(env.app.workspace.dirty, true); assert.equal(env.app.workspace.document.path, "sample.go"); env.app.dispose();
});
