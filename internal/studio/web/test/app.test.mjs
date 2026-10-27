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
  let draft = null, conflict = false;
  let settings = { revision: "s1", title: "Docs", description: "Description", logo: "", favicon: "", footer: "Footer", themeId: "paper", theme: { id: "paper", name: "Paper", mode: "auto", accent: "#315be8", font: "system", density: "comfortable", width: "normal", radius: 10 }, navigation: { showTOC: true, showPreviousNext: false, stickyHeader: true }, editor: { defaultFormat: "markdown", previewOnSave: false }, api: { enabled: true, showCurl: true, requestTimeoutMs: 18000, maxResponseBytes: 65536 }, publishing: { autoPublish: false, gitPushOnPublish: true } };
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
