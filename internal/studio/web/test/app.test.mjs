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
const settle = async () => { for (let i = 0; i < 12; i++) await new Promise(resolve => setImmediate(resolve)); };
const controls = (doc, label) => [...doc.querySelectorAll("button")].filter(node => node.textContent === label);
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
    const body = request.body && typeof request.body === "string" ? JSON.parse(request.body) : request.body;
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
    if (route === "files") return request.method === "POST" ? json({ revision: "p1", published: false, draft: { id: "dir", revision: "d2", baseRevision: "p1", path: body.path, operation: "mkdir" } }, 201) : json({ files: [{ path: "assets/image.png", name: "image.png", directory: false, size: 128, revision: "p1", canWrite: true }], revision: "p1" });
    if (route === "drafts") return json({ drafts: draft ? [draft] : [] });
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
