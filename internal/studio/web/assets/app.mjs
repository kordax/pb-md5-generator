import { API, APIError, Workspace, MAX_SOURCE_BYTES, MAX_UPLOAD_BYTES, can, cleanPath, mergeSettings, formatLanguage, documentProperties, propertiesEqual, validateDocumentProperties, previewDocument } from "./core.mjs";
import { mergeThemeFields, PALETTE_TOKENS } from "./themes.mjs";
import { renderStorage, canManageStorage } from "./storage.mjs";
import { renderNavigation, canManageNavigation } from "./navigation.mjs";
import { UPLOAD_ACCEPT, textFilePath, newTextFilePath, registrationKind, sourceDraftOverlap, registrationReview, registrationRequest, sourceHistorySnapshot, insideFolder, reviewFolderDeletion } from "./files.mjs";

export function boot({ document: doc = globalThis.document, window: win = globalThis.window, fetcher = globalThis.fetch, loadEditor = () => import("./editor.js") } = {}) {
  const $ = id => doc.getElementById(id);
  const h = (tag, attributes = {}, ...children) => {
    const node = doc.createElement(tag);
    for (const [name, value] of Object.entries(attributes)) {
      if (value === undefined || value === null || (value === false && !name.startsWith("aria-"))) continue;
      if (name.startsWith("on")) node.addEventListener(name.slice(2).toLowerCase(), value);
      else if (name === "class") node.className = value;
      else if (["value", "checked", "disabled", "readOnly", "type", "required", "hidden", "title"].includes(name)) node[name] = value;
      else node.setAttribute(name, value === true && !name.startsWith("aria-") ? "" : String(value));
    }
    for (const child of children.flat(Infinity)) if (child !== undefined && child !== null) node.append(typeof child === "object" ? child : doc.createTextNode(String(child)));
    return node;
  };
  const note = message => { $("message").textContent = message; $("message").hidden = !message; };
  function showError(error) {
    if (error?.name === "AbortError") return;
    let message = error?.message || "Something went wrong. Please try again.";
    if (Array.isArray(error?.details?.diagnostics)) message += "\n" + error.details.diagnostics.slice(0, 30).map(item => [item.file, item.line, item.column].filter(Boolean).join(":") + " " + item.message).join("\n");
    if (!user) $("login-error").textContent = message;
    else { $("error").textContent = message; $("error").hidden = false; }
  }
  const safe = action => async event => { try { return await action(event); } catch (error) { showError(error); } };
  const button = (label, action, attributes = {}) => h("button", { type: "button", onclick: safe(action), ...attributes }, label);
  const key = value => encodeURIComponent(value);
  const match = revision => { if (typeof revision !== "string" || !revision || /["\r\n\u0000-\u001f]/.test(revision)) throw new APIError("Refresh this view before making changes."); return '"' + revision + '"'; };
  const nonce = doc.querySelector('meta[name="csp-nonce"]')?.content || "";
  let user = null, documents = [], projectRevision = "", settings = null, themes = [], currentView = "documents", viewSequence = 0, viewLoad = null, filesLoad = null, filesSequence = 0, operationBusy = false, editor = null, editorText = null, formDirty = false, previewLoad = null, previewSequence = 0, editorStateNodes = null, renderedConflict = null;
  const api = new API(new URL("../api/v1/", doc.baseURI), fetcher, () => showLogin("Your session expired. Sign in again to continue."));
  const workspace = new Workspace(api, { confirmDiscard: () => win.confirm("Discard the unsaved changes in this document? Download your draft first if you need a copy."), changed: updateEditorState });
  function source() { return workspace.content; }
  function writable() { return workspace.document && can(user, "write", workspace.document.path) && workspace.document.canWrite !== false; }
  function updateEditorState() {
    if (!editorStateNodes) return;
    const busy = operationBusy || workspace.busy || workspace.loading;
    for (const node of editorStateNodes.mutations) node.disabled = !writable() || busy;
    editorStateNodes.preview.disabled = workspace.loading || !workspace.document;
    editorStateNodes.status.textContent = workspace.loading ? "Opening…" : workspace.busy ? "Saving…" : workspace.dirty ? "Unsaved changes" : workspace.document?.draft ? "Draft saved" : "Published source";
    editorStateNodes.status.className = "badge" + (workspace.dirty ? " warning" : "");
    editorStateNodes.revision.textContent = workspace.document ? "Revision " + workspace.document.revision.slice(0, 16) : "";
    const properties = workspace.properties;
    const language = formatLanguage({ ...workspace.document, ...properties });
    if (editorStateNodes.language !== language) { editor?.setLanguage?.(language); editorStateNodes.language = language; }
    editorStateNodes.foot.textContent = workspace.content.length.toLocaleString() + " characters · " + (workspace.document?.resource === "file" ? "literal text" : properties?.kind || workspace.document?.kind || "") + " · Changes stay in this browser until saved.";
    if (workspace.document?.resource !== "file") $("view-title").textContent = properties?.title || workspace.document?.title || workspace.document?.path;
    for (const field of editorStateNodes.propertiesFields || []) { field.input.disabled = !writable() || workspace.loading; const value = properties?.[field.name] || ""; if (field.input.value !== value) field.input.value = value; }
    if (editorStateNodes.formatting) editorStateNodes.formatting.hidden = (properties?.kind || workspace.document?.kind) !== "markdown" || !writable();
    editor?.setEditable(!!writable() && !workspace.loading);
    if (editorText) editorText.readOnly = !writable() || workspace.loading;
    if (workspace.conflict && renderedConflict !== workspace.conflict) renderConflict();
    if (!workspace.conflict && renderedConflict) { $("conflict-host")?.replaceChildren(); renderedConflict = null; editorStateNodes.syncComparison = null; }
    editorStateNodes.syncComparison?.();
  }
  function destroyEditor() { editor?.destroy(); editor = null; editorText = null; editorStateNodes = null; renderedConflict = null; previewLoad?.abort(); previewLoad = null; previewSequence++; }
  function showLogin(message = "") {
    user = null; api.session(null); api.abortAll(); workspace.dispose(); viewLoad?.abort(); viewSequence++; $("app").hidden = true; $("login-view").hidden = false;
    $("login-progress").textContent = message; $("recover-draft").hidden = !workspace.dirty;
    $("login-form").querySelector('[name="password"]').value = "";
  }
  async function enterSession(session) {
    api.session(session); user = session.user;
    if (!user) { showLogin(); $("setup-notice").hidden = !session.setupRequired; return; }
    $("setup-notice").hidden = true; $("login-view").hidden = true; $("app").hidden = false;
    $("identity").textContent = user.displayName || user.username; $("identity-role").textContent = user.role;
    doc.querySelectorAll("[data-admin]").forEach(node => { node.hidden = !can(user, "admin"); });
    doc.querySelector('[data-view="storage"]').hidden = !canManageStorage(user);
    doc.querySelector('[data-view="navigation"]').hidden = !canManageNavigation(user);
    $("create-document").hidden = user.role === "viewer";
    await Promise.all([refreshDocuments(), loadSettings().then(value => { $("project-name").textContent = value.title; applyAppearance(value.theme); }).catch(error => { if (error.status !== 403) showError(error); })]); await navigate("documents");
  }
  async function mutation(action, success) {
    if (operationBusy || workspace.busy || workspace.loading) throw new APIError("Wait for the current request to finish.", 409, "busy");
    operationBusy = true; updateEditorState();
    try { const result = await action(); if (success) note(typeof success === "function" ? success(result) : success); return result; }
    finally { operationBusy = false; updateEditorState(); }
  }
  async function refreshDocuments() {
    const result = await api.request("documents"); documents = result.documents || []; projectRevision = result.revision || projectRevision; renderTree();
    const referenceLink = $("studio-api-reference");
    if (referenceLink) {
      const reference = documents.find(item => ["openapi", "protobuf"].includes(item.kind) && item.outputPath);
      const siteRoot = new URL("../../", doc.baseURI);
      const target = reference ? new URL(reference.outputPath, siteRoot) : null;
      const available = target && target.origin === siteRoot.origin && target.pathname.startsWith(siteRoot.pathname) && !target.search && !target.hash;
      referenceLink.hidden = !available;
      if (available) referenceLink.setAttribute("href", target.href); else referenceLink.removeAttribute("href");
    }
  }
  function renderTree() {
    const query = $("document-search").value.trim().toLocaleLowerCase(); const root = { folders: new Map(), documents: [] };
    for (const item of documents) {
      if (query && !(item.title + " " + item.path).toLocaleLowerCase().includes(query)) continue;
      const parts = item.path.split("/"); parts.pop(); let folder = root;
      for (const part of parts) { if (!folder.folders.has(part)) folder.folders.set(part, { folders: new Map(), documents: [] }); folder = folder.folders.get(part); }
      folder.documents.push(item);
    }
    const list = folder => h("ul", {}, [...folder.folders.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([name, child]) => h("li", {}, h("details", { open: true }, h("summary", {}, name), list(child)))), folder.documents.map(item => h("li", {}, button([item.title || item.path, item.draft ? " · Draft" : ""].join(""), () => openDocument(item.id), { "aria-current": workspace.document?.id === item.id ? "page" : undefined, title: item.path }))));
    $("document-tree").replaceChildren(list(root));
    if (!root.folders.size && !root.documents.length) $("document-tree").append(h("p", { class: "muted small" }, query ? "No matching documents." : "No documents yet."));
  }
  async function allowViewChange() { if (formDirty && !win.confirm("Discard the unsaved changes in this form?")) return false; formDirty = false; return true; }
  async function navigate(view) {
    if (view === "navigation" && !canManageNavigation(user)) throw new APIError("An administrator with read and write access to the entire project is required.", 403);
    if (view === "storage" && !canManageStorage(user)) throw new APIError("An administrator with read and write access to the entire project is required.", 403);
    if (!await allowViewChange()) return;
    if (["people", "settings", "appearance", "storage", "navigation"].includes(view) && !can(user, "admin")) throw new APIError("An administrator account is required.", 403);
    viewLoad?.abort(); filesLoad?.abort(); filesSequence++; viewLoad = new AbortController(); const sequence = ++viewSequence;
    currentView = view; destroyEditor(); $("error").hidden = true;
    doc.querySelectorAll("[data-view]").forEach(node => { if (node.dataset.view === view) node.setAttribute("aria-current", "page"); else node.removeAttribute("aria-current"); });
    $("view-title").textContent = ({ documents: workspace.document?.title || "Documents", files: "Files", publishing: "Publishing", storage: "Storage", navigation: "Documentation navigation", appearance: "Appearance", settings: "Site settings", people: "People" })[view] || "Workspace";
    const description = $("view-description");
    if (description) description.textContent = ({ documents: workspace.document ? "Edit source, save a draft and publish when it is ready." : "Write and organize your documentation.", files: "Manage the source files behind your published site.", publishing: "Review saved drafts and synchronize your project.", storage: "Connect the storage behind your documentation.", navigation: "Shape how readers explore your documentation.", appearance: "Choose and customize the look of your site.", settings: "Configure your site, authoring and publication.", people: "Manage accounts and access to your workspace." })[view] || "";
    $("view").replaceChildren(h("p", { class: "muted", role: "status" }, "Loading…"));
    const active = () => sequence === viewSequence && !viewLoad.signal.aborted;
    if (view === "documents") { if (workspace.document) await renderEditor(sequence); else renderDocuments(); }
    if (view === "files") await renderFiles(".", active);
    if (view === "publishing") await renderPublishing(active);
    if (view === "storage") await renderStorage({ host: $("view"), api, user, h, button, field, workspace, mutate: mutation, confirm: message => win.confirm(message), active, signal: viewLoad.signal, onDirty: value => { if (active()) formDirty = value; }, onRevision: value => { if (active()) projectRevision = value; }, notify: message => { if (active()) note(message); } });
    if (view === "navigation") await renderNavigation({ host: $("view"), api, user, h, button, field, workspace, mutate: mutation, confirm: message => win.confirm(message), active, signal: viewLoad.signal, onDirty: value => { if (active()) formDirty = value; }, onRevision: value => { if (active()) projectRevision = value; }, onSettings: value => { if (active()) { settings = value; $("project-name").textContent = value.title; applyAppearance(value.theme); } }, notify: message => { if (active()) note(message); } });
    if (view === "settings") await renderSettings(active);
    if (view === "appearance") await renderAppearance(active);
    if (view === "people") await renderPeople(active);
  }
  async function openDocument(id, draft = null) {
    if (!await allowViewChange()) return;
    if (await workspace.open(id, draft ? { draftId: draft.id, draftRevision: draft.revision } : {})) { currentView = "documents"; await navigate("documents"); renderTree(); }
  }
  async function openSource(path, draft = null) {
    if (!await allowViewChange()) return;
    const registered = documents.find(item => item.path === path && can(user, "read", path));
    const selectedDraft = { draftId: draft?.id || "", draftRevision: draft?.revision || "" };
    const opened = registered ? await workspace.open(registered.id, selectedDraft) : await workspace.openFile(path, { ...selectedDraft, canWrite: can(user, "write", path) });
    if (opened) { currentView = "documents"; await navigate("documents"); renderTree(); }
  }
  function renderDocuments() {
    $("view-title").textContent = "Documents";
    if (!documents.length) { $("view").replaceChildren(h("section", { class: "card empty" }, h("h2", {}, "A home for your documentation"), h("p", {}, "Create a guide, reference page or API document to get started."), user.role !== "viewer" ? button("Create document", createDocument, { class: "primary" }) : null)); return; }
    const rows = documents.map(item => h("tr", {}, h("td", {}, button(item.title, () => openDocument(item.id), { class: "quiet" })), h("td", { class: "path" }, item.path), h("td", {}, h("span", { class: "badge" }, item.kind)), h("td", {}, item.draft ? "Draft" : "Published")));
    const mobileRows = h("ul", { class: "mobile-document-list", "aria-label": "All documents" }, documents.map(item => h("li", {}, button(item.title, () => openDocument(item.id), { class: "quiet" }), h("p", { class: "path muted" }, item.path), h("div", { class: "document-row-meta" }, h("span", { class: "badge" }, item.kind), h("span", {}, item.draft ? "Draft" : "Published")))));
    $("view").replaceChildren(h("section", { class: "card documents-panel" }, h("div", { class: "spread" }, h("h2", {}, "All documents"), user.role !== "viewer" ? button("Create document", createDocument, { class: "primary" }) : null), table(["Document", "Source", "Format", "State"], rows), mobileRows));
  }
  async function renderEditor(sequence) {
    const item = workspace.document; if (!item) return renderDocuments(); const sourceFile = item.resource === "file"; $("view-title").textContent = item.title || item.path;
    const state = h("span", { class: "badge" }); const revision = h("span"); const foot = h("div", { class: "editor-foot" });
    const message = h("input", { type: "text", maxlength: 256, placeholder: "Change note (optional)", "aria-label": "Change note" });
    const save = button(settings?.publishing?.autoPublish ? "Save and publish" : "Save draft", async () => { const result = await workspace.save(false, message.value); await refreshDocuments(); note(publicationMessage(result)); if (!sourceFile && settings?.editor?.previewOnSave) await preview(); }, { class: "primary" });
    const publish = button("Publish", async () => { const result = await workspace.save(true, message.value); await refreshDocuments(); message.value = ""; note(publicationMessage(result)); }, { class: "secondary" });
    const previewButton = button("Preview", preview); const history = button("History", () => sourceFile ? renderSourceHistory(item.path) : renderHistory(item.id)); const move = button("Move", () => moveDocument(item)); const remove = button("Delete", () => deleteDocument(item), { class: "danger" });
    const actions = h("div", { class: "actions" }, writable() ? [save, publish] : h("span", { class: "badge" }, "Read only"), sourceFile ? history : [previewButton, history], button("Download source", downloadDraft), writable() && !sourceFile ? [move, remove] : null);
    const host = h("div", { class: "editor-host" });
    const fallback = h("textarea", { class: "source-editor", spellcheck: "false", "aria-label": "Document source", maxlength: MAX_SOURCE_BYTES, value: workspace.content, readOnly: !writable() });
    fallback.addEventListener("input", () => { try { workspace.edit(fallback.value); } catch (error) { fallback.value = workspace.content; showError(error); } });
    host.append(fallback); editorText = fallback;
    const formatting = h("div", { class: "format-toolbar" }, button("Heading", () => insert("## ")), button("Bold", () => insert("**", "**")), button("Link", () => insert("[", "](https://example.com)")), button("Code", () => insert("`", "`")));
    formatting.hidden = item.kind !== "markdown" || !writable();
    const propertiesFields = sourceFile ? [] : [field({ name: "title", label: "Title", required: true, maxlength: 256, value: workspace.properties?.title }), field({ name: "kind", label: "Format", required: true, value: workspace.properties?.kind, options: [["markdown", "Markdown"], ["html", "HTML"], ["protobuf", "Protocol Buffers"], ["openapi", "OpenAPI JSON or YAML"]] }), field({ name: "outputPath", label: "Page URL", required: true, maxlength: 4096, value: workspace.properties?.outputPath, help: "Explicit lowercase relative .html path in the published site. Changes apply on publication." })];
    for (const property of propertiesFields) for (const event of ["input", "change"]) property.input.addEventListener(event, () => { if (!writable()) return; try { workspace.editProperties({ [property.name]: property.input.value }); } catch (error) { property.input.value = workspace.properties?.[property.name] || ""; showError(error); } });
    const propertiesPanel = sourceFile ? null : h("details", { class: "card document-properties" }, h("summary", {}, "Properties"), h("div", { class: "settings-form" }, field({ name: "permanentId", label: "Permanent document ID", value: item.id, readOnly: true }).wrapper, field({ name: "sourcePath", label: "Source path", value: item.path, readOnly: true }).wrapper, ...propertiesFields.map(property => property.wrapper)), h("p", { class: "muted" }, "The ID stays permanent. Use Move for the source path. Properties and source text are saved together using the editor actions."));
    const shell = h("section", { class: "editor-shell", "aria-label": "Document editor" }, h("div", { class: "editor-toolbar" }, actions), h("div", { class: "editor-meta" }, h("span", {}, item.path), state, revision), formatting, host, foot);
    const previewHost = h("section", { id: "preview-host", "aria-label": "Preview" }); const conflict = h("section", { id: "conflict-host" });
    $("view").replaceChildren(...(propertiesPanel ? [propertiesPanel] : []), shell, h("label", {}, "Change note", message), conflict, previewHost);
    editorStateNodes = { mutations: [save, publish, move, remove], preview: previewButton, status: state, revision, foot, propertiesFields, formatting, language: "" }; updateEditorState();
    try {
      const module = await loadEditor(); if (sequence !== viewSequence || currentView !== "documents" || workspace.document?.id !== item.id) return;
      const mount = h("div");
      const enhanced = module.createEditor(mount, { content: workspace.content, language: formatLanguage({ ...item, ...workspace.properties }), editable: !!writable(), nonce, dark: isDark(), onChange: content => workspace.edit(content), onError: showError });
      host.replaceChildren(mount); editorText = null; editor = enhanced; editor.refresh?.(); updateEditorState();
    } catch (_) { if (sequence === viewSequence) note("The enhanced editor could not load. The source editor is still available."); }
    function insert(before, after = "") {
      if (!writable()) return;
      if (editor) editor.replace(before, after);
      else { const from = fallback.selectionStart, to = fallback.selectionEnd; fallback.setRangeText(before + fallback.value.slice(from, to) + after, from, to, "select"); workspace.edit(fallback.value); fallback.focus(); }
    }
    async function preview() {
      const propertiesSnapshot = workspace.properties, properties = workspace.propertiesDirty ? validateDocumentProperties(propertiesSnapshot) : null;
      previewLoad?.abort(); previewLoad = new AbortController(); const run = ++previewSequence; const content = workspace.content; previewButton.disabled = true;
      try {
        const result = await api.request("preview", { method: "POST", body: { documentId: item.id, content, ...(properties ? { properties } : {}) }, signal: previewLoad.signal });
        if (sequence !== viewSequence || run !== previewSequence || content !== workspace.content || !propertiesEqual(propertiesSnapshot, workspace.properties)) return;
        const frame = h("iframe", { class: "preview", title: "Document preview", sandbox: "allow-same-origin", referrerpolicy: "no-referrer" }); frame.srcdoc = previewDocument(result.html, nonce);
        previewHost.replaceChildren(h("h2", {}, "Preview"), frame, ...(result.diagnostics || []).map(diagnostic => h("p", { class: "notice" }, diagnostic.message)));
      } finally { if (sequence === viewSequence && run === previewSequence) previewButton.disabled = false; }
    }
  }
  function renderConflict() {
    const host = $("conflict-host"); if (!host) return; renderedConflict = workspace.conflict;
    const sourceFile = workspace.document?.resource === "file"; let compared = null, comparisonRun = 0, cells = [];
    const latest = h("textarea", { readOnly: true, "aria-label": "Latest published source", rows: 10 }); latest.hidden = true;
    const propertiesHost = h("section", { class: "property-comparison", "aria-label": "Properties comparison", hidden: true }), choiceMessage = h("p", { role: "status" });
    const matches = snapshot => snapshot && snapshot.document === workspace.document && snapshot.sequence === workspace.sequence && $("conflict-host") === host;
    const rebase = button(sourceFile ? "Save merged draft" : "Use my merged draft", async () => {
      if (!matches(compared)) throw new APIError("Compare the latest source again before saving a merged draft.", 409, "conflict");
      if (!writable()) throw new APIError("This document is read only.", 403);
      if (!sourceFile && !compared.choice) throw new APIError("Choose the properties to keep before saving a merged draft.", 409, "conflict");
      if (!win.confirm(sourceFile ? "Save your current text as a draft based on the version you just reviewed? Make sure your text includes the changes you want to keep from the latest published source. Publication remains a separate step." : "Save your current text and the selected title, format and page URL as a draft based on the published version you reviewed? Publication remains a separate step.")) return;
      const revision = compared.revision; compared = null; rebase.hidden = true;
      await workspace.rebase(revision, sourceFile ? {} : { propertiesRevision: revision }); await refreshDocuments(); note(sourceFile ? "Merged source saved as a draft. Publish it from Publishing when ready." : "Your merged source and properties are saved as a draft. Preview and publish them when ready.");
    }, { hidden: true });
    const syncProperties = () => {
      if (sourceFile || !compared) return;
      for (const cell of cells) cell.node.textContent = workspace.properties?.[cell.name] || "";
      if (compared.choice === "published" && !propertiesEqual(workspace.properties, compared.properties)) compared.choice = null;
      if (!compared.choice && (compared.unpublished || propertiesEqual(workspace.properties, compared.properties))) compared.choice = "local";
      rebase.hidden = !writable() || !compared.choice;
      choiceMessage.textContent = compared.choice ? "The current Properties values above will be saved with your merged text." : "Choose local or published properties, then review your merged text before saving.";
    };
    if (editorStateNodes) editorStateNodes.syncComparison = syncProperties;
    const compare = button("Compare latest", async () => {
      const run = ++comparisonRun, snapshot = { document: workspace.document, sequence: workspace.sequence }; compared = null; rebase.hidden = true; propertiesHost.hidden = true;
      const value = await workspace.latest(); if (run !== comparisonRun || !matches(snapshot)) return;
      latest.value = value.content; latest.hidden = false;
      compared = { ...snapshot, revision: sourceFile ? value.revision : value.publishedRevision || value.revision, properties: value.unpublished ? null : documentProperties(value), unpublished: !!value.unpublished, choice: null };
      if (sourceFile) { rebase.hidden = !writable(); return; }
      const names = [["title", "Title"], ["kind", "Format"], ["outputPath", "Page URL"]]; cells = names.map(([name]) => ({ name, node: h("span") }));
      const rows = names.map(([name, label], index) => h("tr", {}, h("th", { scope: "row" }, label), h("td", {}, cells[index].node), h("td", {}, compared.unpublished ? "Not published" : compared.properties[name])));
      propertiesHost.hidden = false;
      propertiesHost.replaceChildren(h("h3", {}, "Published and local properties"), ...(value.unpublished ? [h("p", {}, "This document is not published on this branch. The current project revision was checked; your local text and properties stay selected.")] : []), table(["Property", "Local values", "Published values"], rows), h("div", { class: "actions" }, writable() ? button("Keep my properties", () => { if (matches(compared) && writable()) { compared.choice = "local"; syncProperties(); } }) : null, !value.unpublished && writable() ? button("Use published properties", () => { if (matches(compared) && writable()) { workspace.editProperties(compared.properties); compared.choice = "published"; syncProperties(); } }) : null), choiceMessage);
      syncProperties();
    });
    host.replaceChildren(h("div", { class: "conflict" }, h("h2", {}, "This document changed elsewhere"), h("p", {}, sourceFile ? "Your source is still here. Compare the latest published source, then edit your text above. Save merged draft changes its base after your confirmation and keeps publication separate." : "Your text and properties are still here. Compare the published values below, choose the properties to keep and edit your merged text above. No values are merged automatically."), h("div", { class: "actions" }, compare, rebase, button("Download my draft", downloadDraft), button(sourceFile ? "Reload saved source" : "Reload document", async () => { if (workspace.document?.resource === "file") await openSource(workspace.document.path, workspace.document.draftId ? { id: workspace.document.draftId } : null); else if (await workspace.open(workspace.document.id)) await navigate("documents"); }), sourceFile ? button("Open current published source", () => openSource(workspace.document.path)) : null), propertiesHost, latest));
  }
  function downloadDraft() {
    if (!workspace.document) return; const blob = new Blob([source()], { type: "text/plain;charset=utf-8" }); const url = URL.createObjectURL(blob);
    const link = h("a", { href: url, download: workspace.document.path.split("/").pop() || "draft.txt" }); doc.body.append(link); link.click(); link.remove();
    const timer = setTimeout(() => { URL.revokeObjectURL(url); downloadTimers.delete(timer); }, 1000); downloadTimers.set(timer, url);
  }
  function table(headings, rows) { return h("div", { class: "table-scroll" }, h("table", {}, h("thead", {}, h("tr", {}, headings.map(title => h("th", { scope: "col" }, title)))), h("tbody", {}, rows))); }
  function field(definition) {
    const { name, label, type = "text", value = "", options, help, ...attributes } = definition;
    const input = options ? h("select", { name, ...attributes }, options.map(option => { const [id, title] = Array.isArray(option) ? option : [option, option]; return h("option", { value: id }, title); })) : type === "textarea" ? h("textarea", { name, rows: 4, ...attributes }) : h("input", { name, type, ...attributes });
    if (type === "checkbox") input.checked = !!value; else input.value = value;
    return { wrapper: h("label", {}, label, input, help ? h("small", {}, help) : null), input, type, name };
  }
  function values(fields) { return Object.fromEntries(fields.map(item => [item.name, item.type === "checkbox" ? item.input.checked : item.type === "number" ? Number(item.input.value) : item.input.value])); }
  let dialogBusy = false;
  function dialog(title, definitions, submit, action) {
    $("dialog-title").textContent = title; $("dialog-error").textContent = ""; $("dialog-submit").textContent = submit; const fields = definitions.map(field); $("dialog-fields").replaceChildren(...fields.map(item => item.wrapper));
    $("dialog-form").onsubmit = async event => {
      event.preventDefault(); if (dialogBusy) return;
      const data = values(fields); fields.filter(item => item.type === "password").forEach(item => { item.input.value = ""; }); dialogBusy = true; fields.forEach(item => { item.input.disabled = true; }); $("dialog-submit").disabled = true; $("dialog-cancel").disabled = true;
      try { if (await action(data) !== false) $("action-dialog").close(); } catch (error) { $("dialog-error").textContent = error.message; }
      finally { dialogBusy = false; fields.forEach(item => { item.input.disabled = false; }); $("dialog-submit").disabled = false; $("dialog-cancel").disabled = false; }
    }; $("action-dialog").showModal();
  }
  function createDocument() {
    dialog("Create document", [{ name: "title", label: "Title", required: true, maxlength: 256 }, { name: "path", label: "Source path", required: true, placeholder: "docs/getting-started.md" }, { name: "kind", label: "Format", value: settings?.editor?.defaultFormat || "markdown", options: [["markdown", "Markdown"], ["html", "HTML"], ["protobuf", "Protocol Buffers"], ["openapi", "OpenAPI JSON or YAML"]] }, { name: "outputPath", label: "Page URL", placeholder: "guides/getting-started.html", help: "Optional relative HTML path in the published site." }], settings?.publishing?.autoPublish ? "Create and publish" : "Create draft", async data => {
      if (!await workspace.discardAllowed()) return; cleanPath(data.path); if (data.outputPath) cleanPath(data.outputPath);
      let content = data.kind === "markdown" ? "# " + data.title + "\n\n" : data.kind === "html" ? "<h1>" + data.title.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;") + "</h1>\n" : data.kind === "protobuf" ? 'syntax = "proto3";\n\npackage documentation;\n' : JSON.stringify({ openapi: "3.1.0", info: { title: data.title, version: "1.0.0" }, paths: {} }, null, 2) + "\n";
      if (data.kind === "openapi" && /\.ya?ml$/i.test(data.path)) content = 'openapi: 3.1.0\ninfo:\n  title: ' + JSON.stringify(data.title) + '\n  version: 1.0.0\npaths: {}\n';
      const item = await mutation(() => api.request("documents", { method: "POST", body: { ...data, content, revision: projectRevision } })); await refreshDocuments(); workspace.clear(); await openDocument(item.document?.id || item.id); note(item.published ? publicationMessage(item) : "Document created as a draft.");
    });
  }
  function moveDocument(item) {
    dialog("Move document", [{ name: "to", label: "New source path", required: true, value: item.path }], settings?.publishing?.autoPublish ? "Move and publish" : "Save move as draft", async ({ to }) => {
      if (!await workspace.discardAllowed()) return; cleanPath(to); await mutation(() => api.request("documents/" + key(item.id) + "/move", { method: "POST", body: { to, revision: workspace.document.revision } }), result => result?.published ? "Move published." : "Move saved as a draft. Publish it from Publishing."); workspace.clear(); await refreshDocuments(); await navigate("publishing");
    });
  }
  async function deleteDocument(item) {
    if (!await workspace.discardAllowed() || !win.confirm(settings?.publishing?.autoPublish ? "Delete “" + item.title + "” and publish its removal now?" : "Stage deletion of “" + item.title + "”? The published site changes only when the draft is published.")) return;
    await mutation(() => api.request("documents/" + key(item.id), { method: "DELETE", headers: { "If-Match": match(workspace.document.revision) } }), result => result?.published ? "Deletion published." : "Deletion saved as a draft."); workspace.clear(); await refreshDocuments(); await navigate("publishing");
  }
  async function renderHistory(id) {
    const sequence = ++viewSequence; destroyEditor(); $("view-title").textContent = "Document history";
    const result = await api.request("documents/" + key(id) + "/history"); if (sequence !== viewSequence) return;
    const rows = (result.revisions || []).map(item => h("tr", {}, h("td", {}, item.createdAt ? new Date(item.createdAt).toLocaleString() : "Saved revision"), h("td", {}, item.message || item.commit?.slice(0, 12) || item.id), h("td", {}, item.author || ""), h("td", {}, writable() ? button("Restore as draft", async () => {
      if (!await workspace.discardAllowed() || !win.confirm("Restore this version as a new draft? Publication remains a separate step.")) return;
      const restored = await mutation(() => api.request("documents/" + key(id) + "/restore", { method: "POST", body: { historyId: item.id, revision: workspace.document.revision } })); workspace.clear(); await openDocument(restored.document?.id || id); note("Historical version restored as a draft.");
    }) : null)));
    $("view").replaceChildren(h("section", { class: "card" }, h("div", { class: "spread" }, h("h2", {}, workspace.document?.title || "History"), button("Back to editor", () => navigate("documents"))), rows.length ? table(["Saved", "Change", "Author", ""], rows) : h("p", { class: "muted" }, "No published revisions yet.")));
  }
  async function renderRegistration(path) {
    cleanPath(path);
    if (!textFilePath(path)) throw new APIError("Choose a supported UTF-8 text source.", 415);
    if (!can(user, "read", path) || !can(user, "write", path)) throw new APIError("Read and write access to this source is required.", 403);
    if (!await allowViewChange()) return;
    viewLoad?.abort(); filesLoad?.abort(); filesSequence++; viewLoad = new AbortController(); const controller = viewLoad, sequence = ++viewSequence;
    currentView = "files"; destroyEditor(); $("error").hidden = true; $("view-title").textContent = "Register document";
    const active = () => sequence === viewSequence && !controller.signal.aborted;
    const fields = [field({ name: "title", label: "Title", required: true, maxlength: 256, value: path.split("/").at(-1).replace(/\.[^.]+$/, "") }), field({ name: "kind", label: "Format", required: true, value: registrationKind(path), options: [["markdown", "Markdown"], ["html", "HTML"], ["protobuf", "Protocol Buffers"], ["openapi", "OpenAPI JSON or YAML"]], help: "Choose how this source will be rendered. Registration does not run source code." }), field({ name: "id", label: "Stable document ID", maxlength: 128, help: "Optional. Leave blank to generate a permanent ID." }), field({ name: "outputPath", label: "Page URL", maxlength: 4096, placeholder: "guides/getting-started.html", help: "Optional lowercase ASCII relative .html path. The default is the lowercased source path with .html extension; choose an explicit URL for a source path containing spaces or non-ASCII characters." })];
    const form = h("form", { class: "settings-form registration-form" }, ...fields.map(item => item.wrapper));
    const reviewed = h("textarea", { class: "source-editor", rows: 14, readOnly: true, spellcheck: "false", "aria-label": "Reviewed live source" });
    const status = h("p", { role: "status" }), error = h("p", { class: "notice", role: "alert" }), outcome = h("div", { class: "actions" });
    const save = h("button", { type: "submit", class: "primary", disabled: true }, "Save registration draft");
    let review = null, loading = false, saving = false, completed = false, reviewRun = 0, version = 0;
    const rereview = button("Re-review live source", loadReview);
    const updateButtons = () => { save.disabled = !review || loading || saving || completed; rereview.disabled = loading || saving || completed; };
    const changed = () => { if (active()) { version++; formDirty = true; } };
    form.addEventListener("input", changed); form.addEventListener("change", changed);
    form.addEventListener("submit", safe(async event => {
      event.preventDefault(); if (!active() || loading || saving || completed) return;
      let body; try { body = registrationRequest(review, values(fields)); } catch (failure) { error.textContent = failure.message; return; }
      const submittedVersion = version; saving = true; error.textContent = ""; updateButtons();
      try {
        const saved = await mutation(() => api.request("documents/register", { method: "POST", body }));
        const item = saved?.document, draft = saved?.draft;
        if (saved?.published !== false || saved.revision !== body.revision || !draft?.id || !draft.revision || draft.baseRevision !== body.revision || draft.path !== path || !item?.id || item.path !== path || item.title !== body.title || item.kind !== body.kind || body.id && item.id !== body.id) throw new APIError("The registration response is incomplete. Check Publishing before retrying; your editor is unchanged.");
        if (!active()) return;
        completed = true; if (version === submittedVersion) formDirty = false;
        const { content: _content, ...summary } = item; documents = documents.filter(item => item.id !== summary.id).concat(summary); projectRevision = saved.revision; renderTree();
        status.textContent = version === submittedVersion ? "Registration saved as a draft. The published site and your open editor are unchanged." : "Registration saved with the submitted metadata. Your newer form values remain here, and your open editor is unchanged.";
        outcome.replaceChildren(button("Open document draft", () => openDocument(item.id, draft)), button("Open Publishing", () => navigate("publishing"))); note("Document registration saved as a draft.");
      } catch (failure) {
        if (!active()) return;
        review = null; status.textContent = "Registration was not confirmed. Check Publishing, then explicitly re-review the live source before another attempt."; error.textContent = failure.message;
        outcome.replaceChildren(button("Open Publishing", () => navigate("publishing")));
      } finally { saving = false; if (active()) updateButtons(); }
    }));
    form.append(h("div", { class: "actions" }, save, rereview));
    $("view").replaceChildren(h("section", { class: "card" }, h("div", { class: "spread" }, h("h2", { class: "path" }, path), h("div", { class: "actions" }, button("Cancel registration", () => navigate("files")), workspace.document ? button("Back to editor", () => navigate("documents")) : null)), h("p", {}, "Review the published source below, then choose its document metadata. Unsaved text in your editor is kept separately. Registration always creates a draft; publication remains a separate step."), form, status, error, outcome), h("section", { class: "card" }, h("h2", {}, "Published source review"), h("p", { class: "muted" }, "This read-only text is the source bound to the registration request. Re-review after any project or source conflict."), reviewed));
    await loadReview();
    async function loadReview() {
      if (!active() || saving || completed) return;
      const run = ++reviewRun; review = null; loading = true; status.textContent = "Loading the current published source…"; error.textContent = ""; updateButtons();
      try {
        const [source, catalog, drafts] = await Promise.all([api.source(path, { signal: controller.signal }), api.request("documents?published=true", { signal: controller.signal }), api.request("drafts", { signal: controller.signal })]);
        if (!active() || run !== reviewRun) return;
        if (!Array.isArray(drafts?.drafts)) throw new APIError("The saved changes response is incomplete. Re-review this source.");
        review = registrationReview(path, source, catalog, drafts.drafts); reviewed.value = review.content;
        status.textContent = review.pendingChanges ? "Published source reviewed. Saved changes are visible at this path and may belong to another branch. Overlapping changes on the active branch must be published or discarded before registration." : "Published source reviewed. Saving registers these exact bytes as a draft and does not publish them.";
      } catch (failure) { if (active() && run === reviewRun && failure?.name !== "AbortError") { review = null; status.textContent = "The live source has not been reviewed successfully."; error.textContent = failure.message; } }
      finally { if (run === reviewRun) loading = false; if (active()) updateButtons(); }
    }
  }
  function createTextFile(folder, files, drafts, revision, active) {
    dialog("Create text file", [{ name: "name", label: "File name", required: true, maxlength: 255, help: "A supported text extension is required. The new file stays in the editor until you save it." }], "Open editor", async data => {
      if (!active()) throw new APIError("Refresh the folder before creating a file.", 409, "conflict");
      const path = newTextFilePath(data.name, folder, files, drafts);
      if (!can(user, "read", path) || !can(user, "write", path)) throw new APIError("You cannot create a file at this path.", 403);
      if (!await workspace.createFile(path, { revision })) return false;
      currentView = "documents"; await navigate("documents"); renderTree(); return true;
    });
  }
  function sourceHistoryDialog(folder = ".") {
    dialog("File history", [{ name: "path", label: "Exact file path", required: true, maxlength: 4096, value: folder === "." ? "" : folder + "/", help: "Enter a known file path, including a file that has been deleted." }], "View history", data => renderSourceHistory(data.path));
  }
  async function renderSourceHistory(path) {
    cleanPath(path); if (!can(user, "read", path)) throw new APIError("You cannot read history at this path.", 403);
    if (!await allowViewChange()) return false;
    viewLoad?.abort(); filesLoad?.abort(); filesSequence++; viewLoad = new AbortController(); const controller = viewLoad, sequence = ++viewSequence;
    currentView = "files"; destroyEditor(); $("error").hidden = true; $("view-title").textContent = "File history";
    doc.querySelectorAll("[data-view]").forEach(node => { if (node.dataset.view === "files") node.setAttribute("aria-current", "page"); else node.removeAttribute("aria-current"); });
    const active = () => sequence === viewSequence && !controller.signal.aborted;
    $("view").replaceChildren(h("p", { class: "muted" }, "Loading file history…"));
    const result = await api.request("files/history?path=" + key(path), { signal: controller.signal }); if (!active()) return false;
    const snapshot = sourceHistorySnapshot(result, path), status = h("p", { role: "status" }), outcome = h("div", { class: "actions" });
    const restoreButtons = []; let pending = false, completed = false, stale = false;
    const rows = snapshot.revisions.map(item => {
      const restore = can(user, "write", path) && item.restorable && !item.deleted ? button("Restore as draft", async () => {
        if (!active() || pending || completed || stale || !win.confirm("Restore the contents of “" + path + "” from this version as a new draft? Current document registration stays as it is. A deleted document entry will not be recreated. Your current editor stays open and publication remains a separate step.")) return;
        pending = true; restoreButtons.forEach(node => { node.disabled = true; });
        try {
          const saved = await mutation(() => api.request("files/restore", { method: "POST", body: { path, historyId: item.id, revision: snapshot.revision } }));
          if (saved?.published !== false || !saved.draft?.id || !saved.draft.revision || saved.draft.path !== path || !saved.draft.baseRevision) throw new APIError("The restore response is incomplete. Check Publishing before retrying; your editor is unchanged.");
          completed = true; if (!active()) return;
          status.textContent = "File contents restored as a new draft. Your editor and the published files are unchanged.";
          outcome.replaceChildren(textFilePath(path) ? button("Open restored draft", () => openSource(path, saved.draft)) : null, button("Open Publishing", () => navigate("publishing"))); note("File contents restored as a draft.");
        } catch (error) {
          stale = true; if (active()) status.textContent = "Restoration was not confirmed. Your editor is unchanged. Check Publishing and refresh this history before trying again."; throw error;
        } finally { pending = false; if (active() && !completed && !stale) restoreButtons.forEach(node => { node.disabled = false; }); }
      }) : null;
      if (restore) restoreButtons.push(restore);
      const date = new Date(item.createdAt);
      return h("tr", {}, h("td", {}, Number.isNaN(date.valueOf()) ? "Saved revision" : date.toLocaleString()), h("td", {}, item.deleted ? "Deleted" : item.size.toLocaleString() + " bytes"), h("td", {}, item.author || "Unknown author"), h("td", {}, restore));
    });
    $("view").replaceChildren(h("section", { class: "card" }, h("div", { class: "spread" }, h("h2", { class: "path" }, path), h("div", { class: "actions" }, button("Choose file path", () => sourceHistoryDialog()), button("Refresh history", () => renderSourceHistory(path)), button("Back to files", () => navigate("files")), workspace.document ? button("Back to editor", () => navigate("documents")) : null)), h("p", { class: "muted" }, "History is limited to this exact path. Restoring copies file contents into a new draft and preserves current document registration. It does not recreate a deleted document entry."), rows.length ? table(["Saved", "Contents", "Author", ""], rows) : h("p", {}, "No retained published versions were found for this path."), status, outcome)); return true;
  }
  async function renderFiles(path = ".", active = () => true) {
    filesLoad?.abort(); const controller = new AbortController(); filesLoad = controller; const run = ++filesSequence; const parentSignal = viewLoad?.signal; const abort = () => controller.abort();
    if (parentSignal?.aborted) abort(); else parentSignal?.addEventListener("abort", abort, { once: true });
    let result, draftResult; try { [result, draftResult] = await Promise.all([api.request("files?path=" + key(path), { signal: controller.signal }), api.request("drafts", { signal: controller.signal })]); } finally { parentSignal?.removeEventListener("abort", abort); }
    if (!active() || run !== filesSequence || controller.signal.aborted) return; projectRevision = result.revision || projectRevision;
    const drafts = draftResult.drafts || [], reviewHost = h("section", { "aria-label": "Folder deletion" }); let reviewSequence = 0;
    const showDeleteReview = async item => {
      const current = ++reviewSequence, valid = () => active() && run === filesSequence && current === reviewSequence && !controller.signal.aborted;
      await reviewFolderDeletion({ host: reviewHost, folder: item.path, revision: item.revision, user, api, h, button, workspace, mutate: mutation, confirm: message => win.confirm(message), active: valid, signal: controller.signal, refresh: () => renderFiles(path, active), returnToEditor: () => navigate("documents"), downloadSource: downloadDraft, onStaged: async () => { if (!valid()) return; note("Folder deletion saved as a draft. Published files are unchanged. Review and publish it from Publishing."); await navigate("publishing"); } });
    };
    const upload = h("input", { type: "file", multiple: true, accept: UPLOAD_ACCEPT, "aria-label": "Upload attachments" });
    let uploading = false;
    upload.addEventListener("change", safe(async () => {
      if (uploading) return;
      const files = Array.from(upload.files || []); if (files.length > 32) throw new APIError("Upload up to 32 files at a time.");
      const destinations = new Set();
      for (const file of files) {
        const destination = cleanPath(path === "." ? file.name : path + "/" + file.name), limit = textFilePath(file.name) ? MAX_SOURCE_BYTES : MAX_UPLOAD_BYTES;
        if (!UPLOAD_ACCEPT.split(",").includes("." + file.name.split(".").at(-1).toLowerCase())) throw new APIError("This file extension is not supported.", 415);
        if (file.size > limit) throw new APIError("“" + file.name + "” exceeds the " + (limit === MAX_SOURCE_BYTES ? "4 MiB text" : "16 MiB file") + " limit.");
        if (!can(user, "write", destination)) throw new APIError("You cannot upload to this folder.", 403);
        if (destinations.has(destination)) throw new APIError("Select only one version of each filename."); destinations.add(destination);
        if (drafts.some(draft => draft.path === destination || draft.to === destination)) throw new APIError("A saved change already targets “" + destination + "”. Open or discard it from Publishing before replacing it.", 409, "conflict");
      }
      uploading = true; upload.disabled = true; let staged = 0, published = 0, uploadRevision = result.revision;
      try {
        for (const file of files) {
          if (file.size > MAX_UPLOAD_BYTES) throw new APIError("“" + file.name + "” exceeds the 16 MiB file limit.");
          const destination = cleanPath(path === "." ? file.name : path + "/" + file.name);
          if (!can(user, "write", destination)) throw new APIError("You cannot upload to this folder.", 403);
          if (result.files?.some(item => item.path === destination) && !win.confirm("Replace “" + destination + "” with the selected file?")) continue;
          const saved = await mutation(() => api.request("files/content?path=" + key(destination), { method: "PUT", raw: true, body: file, headers: { "Content-Type": "application/octet-stream", "If-Match": match(uploadRevision) } }));
          if (saved?.published) published++; else staged++; uploadRevision = saved?.revision || uploadRevision; projectRevision = uploadRevision;
        }
        note(staged + " file(s) saved as drafts; " + published + " file(s) published." + (staged ? " Open Publishing to publish the saved drafts." : ""));
        if (run === filesSequence && active()) await renderFiles(path, active);
      } catch (error) { if (staged || published) note(staged + " file(s) saved as drafts; " + published + " file(s) published before the upload stopped."); throw error; }
      finally { uploading = false; upload.disabled = false; }
    }));
    const controls = h("div", { class: "actions" }, path !== "." ? button("Parent folder", () => renderFiles(path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ".", active)) : null, button("File history", () => sourceHistoryDialog(path)), user.role !== "viewer" ? button("Create text file", () => createTextFile(path, result.files || [], drafts, result.revision, () => active() && run === filesSequence && !controller.signal.aborted)) : null, user.role !== "viewer" ? button("New folder", () => dialog("Create folder", [{ name: "path", label: "Folder path", required: true, value: path === "." ? "" : path + "/" }], settings?.publishing?.autoPublish ? "Create and publish" : "Create draft", async data => { cleanPath(data.path); await mutation(() => api.request("files", { method: "POST", body: { path: data.path, revision: projectRevision } }), result => result?.published ? "Folder published." : "Folder saved as a draft. Publish it from Publishing."); if (run === filesSequence && active()) await renderFiles(path, active); })) : null);
    const items = new Map((result.files || []).map(item => [item.path, { ...item, drafts: [] }]));
    for (const draft of drafts) {
      if (draft.operation !== "write" || !textFilePath(draft.path) || !can(user, "read", draft.path)) continue;
      const parent = draft.path.includes("/") ? draft.path.slice(0, draft.path.lastIndexOf("/")) : "."; if (parent !== path) continue;
      if (!items.has(draft.path)) items.set(draft.path, { path: draft.path, name: draft.path.split("/").at(-1), directory: false, revision: result.revision, draftOnly: true, drafts: [] });
      items.get(draft.path).drafts.push(draft);
    }
    const rows = [...items.values()].map(item => {
      const write = can(user, "write", item.path) && item.canWrite !== false, actions = [];
      if (!item.directory && !item.draftOnly) actions.push(h("a", { class: "button", href: new URL("files/content?path=" + key(item.path), api.base).href, download: "" }, "Download"));
      if (!item.directory && textFilePath(item.path)) {
        if (!item.draftOnly) actions.push(button("Open source", () => openSource(item.path)));
        for (const draft of item.drafts) actions.push(button(item.drafts.length > 1 ? "Open draft “" + draft.id.slice(0, 12) + "”" : "Open saved draft", () => openSource(item.path, draft)), item.draftOnly ? h("a", { class: "button", href: new URL("files/content?path=" + key(item.path) + "&draftId=" + key(draft.id), api.base).href, download: "" }, "Download draft") : null);
      }
      if (!item.directory) actions.push(button("History", () => renderSourceHistory(item.path)));
      if (!item.directory && textFilePath(item.path) && write && can(user, "read", item.path) && !documents.some(document => document.path === item.path)) {
        const pending = item.draftOnly, overlaps = sourceDraftOverlap(item.path, drafts);
        actions.push(button("Register document", () => renderRegistration(item.path), { disabled: !!pending }), pending ? h("small", { class: "muted" }, "Publish or discard the saved source draft before registering a live file.") : overlaps ? h("small", { class: "muted" }, "Saved changes are visible at this path. Registration checks overlapping changes on the active branch.") : null);
      }
      if (write && !item.draftOnly) {
        actions.push(button("Move", () => dialog("Move file", [{ name: "to", label: "New path", required: true, value: item.path }], settings?.publishing?.autoPublish ? "Move and publish" : "Save move as draft", async data => { cleanPath(data.to); await mutation(() => api.request("files/move", { method: "POST", body: { path: item.path, to: data.to, revision: item.revision } }), result => result?.published ? "Move published." : "Move saved as a draft."); if (run === filesSequence && active()) await renderFiles(path, active); })));
        if (item.directory) { if (can(user, "read", item.path)) actions.push(button("Review folder deletion", () => showDeleteReview(item), { class: "danger" })); }
        else actions.push(button("Delete", async () => { if (!win.confirm(settings?.publishing?.autoPublish ? "Delete “" + item.path + "” and publish its removal now?" : "Stage deletion of “" + item.path + "”?")) return; await mutation(() => api.request("files/content?path=" + key(item.path), { method: "DELETE", headers: { "If-Match": match(item.revision) } }), result => result?.published ? "Deletion published." : "Deletion saved as a draft."); if (run === filesSequence && active()) await renderFiles(path, active); }, { class: "danger" }));
      }
      return h("tr", {}, h("td", { class: "path" }, item.directory ? button((item.name || item.path) + "/", () => renderFiles(item.path, active), { class: "quiet" }) : item.name || item.path), h("td", {}, item.directory ? "Folder" : item.draftOnly ? "Saved source draft" : (item.size || 0).toLocaleString() + " bytes"), h("td", {}, h("div", { class: "actions" }, actions)));
    });
    $("view").replaceChildren(h("section", { class: "card" }, h("div", { class: "spread" }, h("h2", {}, path === "." ? "Content files" : path), controls), h("p", { class: "muted small" }, settings?.publishing?.autoPublish ? "Automatic publishing is enabled. File changes update the published site after validation. Folder deletion is always a separate draft after a reviewed plan." : "File changes are drafts until you publish them. Images and PDFs can be up to 16 MiB; documentation source and text files can be up to 4 MiB. Folder deletion is always a separate draft after a reviewed plan."), user.role !== "viewer" ? h("label", {}, "Upload files", upload) : null, rows.length ? table(["Name", "Size", "Actions"], rows) : h("p", { class: "muted" }, "This folder is empty.")), reviewHost);
  }
  function appearanceMessage(message) { return result => result?.sync && (result.sync.retryable || result.sync.pendingPush || result.sync.state === "pending" || result.sync.error) ? message.replace(/\.$/, "") + " locally. " + (result.sync.error || "Git synchronization remains pending. Retry the push from Publishing.") : message; }
  function publicationMessage(result) { return result?.published ? (result.sync?.pendingPush ? "Published locally. The repository still needs to be pushed." : "Documentation published.") : "Draft saved. The published site has not changed."; }
  function synchronizationMessage(action, result) {
    const failure = result?.lastError || result?.error;
    if (failure || result?.state === "error") return "Repository synchronization did not finish. " + (failure || "Local commits are retained; retry the failed connection from Storage.");
    if (action === "push" && (result?.pendingPush || result?.retryable || result?.state === "pending")) return "Local commits are retained. Some push targets remain pending; retry them from Storage.";
    return "Repository synchronization completed.";
  }
  async function renderPublishing(active) {
    const [draftResult, status] = await Promise.all([api.request("drafts", { signal: viewLoad.signal }), api.request("sync/status", { signal: viewLoad.signal })]); if (!active()) return; projectRevision = status.revision || projectRevision;
    const summary = h("dl", { class: "status-grid" }, [["Storage", status.backend === "git" ? "Git repository" : "Local files"], ["Branch", status.branch || "—"], ["Ahead", status.ahead ?? "—"], ["Behind", status.behind ?? "—"], ["Push", status.pendingPush ? "Pending" : "Up to date"]].map(([label, value]) => h("div", {}, h("dt", {}, label), h("dd", {}, value))));
    const rows = (draftResult.drafts || []).map(draft => h("tr", {}, h("td", { class: "path" }, draft.path), h("td", {}, draft.operation === "delete-tree" ? "Delete entire folder" : draft.operation), h("td", {}, draft.updatedAt ? new Date(draft.updatedAt).toLocaleString() : ""), h("td", {}, h("div", { class: "actions" }, can(user, "write", draft.path) ? [draft.operation === "write" && textFilePath(draft.path) ? button("Open source draft", () => openSource(draft.path, draft)) : null, button("Publish", async () => { if (draft.operation === "delete-tree" && !win.confirm("Publish deletion of the entire folder “" + draft.path + "” now?" + (insideFolder(workspace.document?.path, draft.path) ? " Your open source is inside this folder; its text will be retained in the editor." : ""))) return; const result = await mutation(() => api.request("drafts/" + key(draft.id) + "/publish", { method: "POST", body: { revision: draft.revision, message: "", push: false } }), publicationMessage); await refreshDocuments(); await renderPublishing(active); if (workspace.document?.draftId === draft.id && !workspace.dirty) workspace.clear(); return result; }, { class: "primary" }), button("Discard", async () => { if (workspace.document?.draftId === draft.id && !await workspace.discardAllowed()) return; if (!win.confirm("Discard this saved draft? Published content will not change.")) return; const openDocument = workspace.document, openContent = workspace.content, openSequence = workspace.sequence; await mutation(() => api.request("drafts/" + key(draft.id), { method: "DELETE", headers: { "If-Match": match(draft.revision) } }), "Draft discarded."); if (workspace.document?.draftId === draft.id && workspace.document === openDocument && workspace.content === openContent && workspace.sequence === openSequence) workspace.clear(); await refreshDocuments(); await renderPublishing(active); }, { class: "danger" })] : h("span", { class: "muted" }, "Read only")))));
    const storage = h("section", { class: "card" }, h("h2", {}, "Storage and synchronization"), summary, status.lastError ? h("p", { class: "notice" }, status.lastError) : null);
    if (canManageStorage(user)) storage.append(button("Manage storage connections", () => navigate("storage")));
    if (status.backend === "git" && can(user, "admin")) {
      storage.append(h("div", { class: "actions" }, ["fetch", "pull", "push"].map(action => button(({ fetch: "Fetch remote state", pull: "Pull updates", push: "Push commits" })[action], async () => { if (action === "pull" && !await workspace.discardAllowed()) return; await mutation(() => api.request("sync", { method: "POST", body: { action, revision: projectRevision } }), result => synchronizationMessage(action, result)); if (action === "pull") workspace.clear(); await refreshDocuments(); await renderPublishing(active); }))));
      const result = await api.request("branches", { signal: viewLoad.signal }); if (!active()) return; const select = h("select", { "aria-label": "Repository branch" }, (result.branches || []).map(branch => h("option", { value: branch.name }, branch.name))); select.value = result.branches?.find(branch => branch.current)?.name || status.branch || "";
      storage.append(h("h3", {}, "Branches"), h("div", { class: "actions" }, select, button("Switch branch", async () => { if (!await workspace.discardAllowed()) return; await mutation(() => api.request("branches/checkout", { method: "POST", body: { name: select.value, revision: projectRevision } }), "Branch selected."); workspace.clear(); await refreshDocuments(); await renderPublishing(active); }), button("New branch", () => dialog("Create branch", [{ name: "name", label: "Branch name", required: true, maxlength: 200 }], "Create branch", async data => { await mutation(() => api.request("branches", { method: "POST", body: { name: data.name, revision: projectRevision } }), "Branch created."); await renderPublishing(active); }))));
    }
    $("view").replaceChildren(h("section", { class: "card" }, h("div", { class: "spread" }, h("h2", {}, "Saved drafts"), button("Refresh", () => renderPublishing(active))), rows.length ? table(["Source", "Change", "Saved", "Actions"], rows) : h("p", { class: "muted" }, "No saved drafts. Save a document or upload files to prepare a publication.")), storage);
  }
  async function loadSettings() { settings = await api.request("settings"); return settings; }
  function isDark() { return doc.documentElement.dataset.mode === "dark" || (doc.documentElement.dataset.mode !== "light" && win.matchMedia("(prefers-color-scheme: dark)").matches); }
  function applyAppearance(theme) {
    const root = doc.documentElement; root.dataset.mode = ["light", "dark"].includes(theme?.mode) ? theme.mode : "auto"; root.dataset.font = ["serif", "mono"].includes(theme?.font) ? theme.font : "system"; root.dataset.density = theme?.density === "compact" ? "compact" : "comfortable"; root.dataset.width = theme?.width === "wide" ? "wide" : "normal";
    const palette = theme?.[isDark() ? "dark" : "light"] || {};
    for (const [token, property] of Object.entries({ background: "--bg", surface: "--panel", text: "--text", muted: "--muted", border: "--line", accent: "--accent" })) {
      const color = palette[token] || (token === "accent" ? theme?.accent : "");
      if (/^#[0-9a-fA-F]{6}$/.test(color || "")) root.style.setProperty(property, color); else root.style.removeProperty(property);
    }
    if (Number.isInteger(theme?.radius) && theme.radius >= 0 && theme.radius <= 24) root.style.setProperty("--radius", theme.radius + "px"); editor?.setDark(isDark());
  }
  async function renderSettings(active) {
    let original = await loadSettings(); if (!active()) return;
    const definitions = [
      { name: "title", label: "Site title", value: original.title, required: true, maxlength: 256 }, { name: "description", label: "Description", value: original.description || "", maxlength: 4096 }, { name: "logo", label: "Logo asset path", value: original.logo || "" }, { name: "favicon", label: "Favicon asset path", value: original.favicon || "" }, { name: "footer", label: "Footer", type: "textarea", value: original.footer || "", maxlength: 1024 },
      { name: "navigation.showTOC", label: "Show table of contents", type: "checkbox", value: original.navigation?.showTOC ?? true }, { name: "navigation.showPreviousNext", label: "Show previous and next pages", type: "checkbox", value: original.navigation?.showPreviousNext ?? true }, { name: "navigation.stickyHeader", label: "Keep header visible while reading", type: "checkbox", value: original.navigation?.stickyHeader ?? true },
      { name: "editor.defaultFormat", label: "New document format", value: original.editor?.defaultFormat || "markdown", options: ["markdown", "html", "protobuf", "openapi"] }, { name: "editor.previewOnSave", label: "Preview after saving a draft", type: "checkbox", value: original.editor?.previewOnSave || false },
      { name: "api.enabled", label: "Enable API request client", type: "checkbox", value: original.api?.enabled ?? true }, { name: "api.showCurl", label: "Show cURL examples", type: "checkbox", value: original.api?.showCurl ?? true }, { name: "api.requestTimeoutMs", label: "API request timeout (milliseconds)", type: "number", value: original.api?.requestTimeoutMs || 30000, min: 1000, max: 30000 }, { name: "api.maxResponseBytes", label: "Maximum API response preview (bytes)", type: "number", value: original.api?.maxResponseBytes || 1048576, min: 1024, max: 1048576 },
      { name: "publishing.autoPublish", label: "Publish valid changes automatically", type: "checkbox", value: original.publishing?.autoPublish || false }, { name: "publishing.gitPushOnPublish", label: "Push Git commits after publication", type: "checkbox", value: original.publishing?.gitPushOnPublish || false },
    ];
    const fields = definitions.map(field);
    const groups = [["Site identity", 0, 5], ["Reading", 5, 8], ["Authoring", 8, 10], ["API requests", 10, 14], ["Publishing", 14, 16]];
    const sections = groups.map(([title, start, end]) => h("fieldset", { class: "settings-section" }, h("legend", {}, title), h("div", { class: "settings-grid" }, fields.slice(start, end).map(item => item.wrapper))));
    const form = h("form", { class: "settings-form card" }, h("h2", {}, "General and reading settings"), h("p", { class: "muted small" }, "Changes are saved together. Appearance is managed separately."), sections, h("div", { class: "actions" }, h("button", { type: "submit", class: "primary" }, "Save settings")));
    form.addEventListener("input", () => { formDirty = true; }); form.addEventListener("change", () => { formDirty = true; });
    form.addEventListener("submit", safe(async event => {
      event.preventDefault(); const data = values(fields); const changes = {};
      for (const [name, value] of Object.entries(data)) { const [group, property] = name.split("."); if (property) changes[group] = { ...(changes[group] || original[group] || {}), [property]: value }; else changes[group] = value; }
      const submit = form.querySelector('button[type="submit"]'); submit.disabled = true; fields.forEach(item => { item.input.disabled = true; });
      try {
        settings = await mutation(() => api.request("settings", { method: "PUT", body: mergeSettings(original, changes) }), appearanceMessage("Site settings saved.")); original = settings; formDirty = false; $("project-name").textContent = settings.title; applyAppearance(settings.theme);
      } finally { submit.disabled = false; fields.forEach(item => { item.input.disabled = false; }); }
    }));
    if (canManageNavigation(user)) form.querySelector(".actions").append(button("Edit documentation navigation", () => navigate("navigation")));
    $("view").replaceChildren(form);
  }
  function themeFields(theme = {}) {
    return [{ name: "id", label: "Theme ID", value: theme.id || "", required: true, pattern: "[a-z0-9][a-z0-9_-]{0,63}", readOnly: !!theme.id }, { name: "name", label: "Name", value: theme.name || "", required: true, maxlength: 120 }, { name: "description", label: "Description", value: theme.description || "", maxlength: 512 }, { name: "mode", label: "Color mode", value: theme.mode || "auto", options: [["auto", "System preference"], ["light", "Light"], ["dark", "Dark"]] }, { name: "accent", label: "Accent color", type: "color", value: theme.accent || "#6557d2" }, { name: "font", label: "Font", value: theme.font || "system", options: ["system", "serif", "mono"] }, { name: "density", label: "Spacing", value: theme.density || "comfortable", options: ["comfortable", "compact"] }, { name: "width", label: "Reading width", value: theme.width || "normal", options: ["normal", "wide"] }, { name: "radius", label: "Corner radius", type: "number", value: theme.radius ?? 5, min: 0, max: 24 }, ...["light", "dark"].flatMap(mode => PALETTE_TOKENS.map(token => ({ name: mode + "." + token, label: (mode === "light" ? "Light" : "Dark") + " palette: " + token, value: theme[mode]?.[token] || "", placeholder: "Inherit theme default", pattern: "#[0-9a-fA-F]{6}", maxlength: 7, ...(token === "background" ? { help: "Optional #RRGGBB colors. Leave blank to use the theme defaults." } : {}) })))];
  }
  function themePreview(theme, mode) {
    const defaults = mode === "dark" ? { background: "#191a1f", surface: "#222329", text: "#e4e4e7", muted: "#a1a1aa", border: "#37383f" } : { background: "#ffffff", surface: "#fafafa", text: "#18181b", muted: "#71717a", border: "#e4e4e7" };
    const validColor = value => /^#[0-9a-fA-F]{6}$/.test(value || "");
    const preview = h("div", { class: "theme-preview", "aria-hidden": true }, h("div", { class: "theme-preview-header" }, "PB Docs"), h("div", { class: "theme-preview-body" }, h("div", { class: "theme-preview-rail" }, h("span", { class: "theme-preview-selected" }, "Guides"), h("span", {}, "API"), h("span", {}, "Schemas")), h("div", { class: "theme-preview-page" }, h("strong", {}, "Overview"), h("span", { class: "theme-preview-copy" }, "Your documentation."), h("code", {}, h("span", {}, "GET"), " /pets/{id}"))));
    for (const token of PALETTE_TOKENS) {
      const fallback = token === "accent" ? (validColor(theme.accent) ? theme.accent : "#6557d2") : defaults[token];
      const value = theme[mode]?.[token];
      preview.style.setProperty("--preview-" + token, validColor(value) ? value : fallback);
    }
    return h("figure", {}, preview, h("figcaption", {}, mode === "dark" ? "Dark" : "Light"));
  }
  async function renderAppearance(active) {
    const [original, result] = await Promise.all([loadSettings(), api.request("themes", { signal: viewLoad.signal })]); if (!active()) return; themes = result.themes || [];
    const edit = theme => dialog(theme?.id ? "Edit theme" : "Create theme", themeFields(theme), "Save theme", async data => { const body = mergeThemeFields(theme || {}, data); await mutation(() => api.request("themes", { method: "POST", body }), appearanceMessage("Theme saved.")); await renderAppearance(active); });
    const cards = themes.map(theme => {
      const previews = h("div", { class: "theme-previews" }, themePreview(theme, "light"), themePreview(theme, "dark")); const selected = original.themeId === theme.id;
      const heading = h("div", { class: "theme-card-heading" }, h("h3", {}, theme.name), h("span", { class: "badge" }, ({ light: "Light", dark: "Dark" })[theme.mode] || "System"));
      return h("section", { class: "theme-card", "data-theme-id": theme.id }, previews, heading, h("p", {}, theme.description || ""), selected ? h("p", {}, h("span", { class: "badge success" }, "Current theme")) : null, h("div", { class: "actions" }, button("Use theme", async () => { const { builtin: _builtin, ...value } = theme; settings = await mutation(() => api.request("settings", { method: "PUT", body: mergeSettings(original, { themeId: theme.id, theme: value }) }), appearanceMessage("Theme applied.")); formDirty = false; applyAppearance(settings.theme); await renderAppearance(active); }, { disabled: selected, class: "primary" }), button("Duplicate", () => { const { id: _id, builtin: _builtin, revision: _revision, ...copy } = theme; edit({ ...copy, name: theme.name + " copy" }); }), !theme.builtin ? [button("Edit", () => edit(theme)), button("Delete", async () => { if (!win.confirm("Delete this custom theme?")) return; await mutation(() => api.request("themes/" + key(theme.id), { method: "DELETE", headers: theme.revision ? { "If-Match": match(theme.revision) } : {} }), appearanceMessage("Theme deleted.")); await renderAppearance(active); }, { class: "danger", disabled: selected })] : null));
    });
    $("view").replaceChildren(h("section", { class: "card" }, h("div", { class: "spread" }, h("h2", {}, "Installed themes"), button("Create theme", () => edit(), { class: "primary" })), h("p", { class: "muted" }, "Compare the light and dark palettes. Apply a theme, or duplicate one to make it your own."), h("div", { class: "theme-grid" }, cards)));
  }
  function grantsFields(value) {
    return [{ name: "customGrants", label: "Limit access to specific source paths", type: "checkbox", value: value != null }, { name: "readPrefixes", label: "Readable path prefixes", type: "textarea", value: value?.read?.join("\n") || "", help: "One path per line, such as docs/guides. Use . for all paths. Empty lists deny all content when limits are enabled." }, { name: "writePrefixes", label: "Writable path prefixes", type: "textarea", value: value?.write?.join("\n") || "", help: "A viewer remains read only, even when writable paths are listed." }];
  }
  function parseGrants(data) { if (!data.customGrants) return null; const parse = value => [...new Set(value.split(/\r?\n/).map(item => item.trim()).filter(Boolean).map(item => item === "." ? "." : cleanPath(item)))]; return { read: parse(data.readPrefixes), write: parse(data.writePrefixes) }; }
  function passwordValid(password) { if ([...password].length < 10 || new TextEncoder().encode(password).length > 1024) throw new APIError("Use at least 10 characters and at most 1024 UTF-8 bytes for the password."); }
  async function renderPeople(active) {
    const result = await api.request("users", { signal: viewLoad.signal }); if (!active()) return;
    const edit = person => dialog(person ? "Edit account" : "Create account", [...(!person ? [{ name: "username", label: "Username", required: true, maxlength: 64 }] : []), { name: "displayName", label: "Display name", value: person?.displayName || "", maxlength: 120 }, { name: "role", label: "Role", value: person?.role || "viewer", options: [["viewer", "Viewer — read documentation"], ["editor", "Editor — write and publish documents"], ["admin", "Administrator — manage the workspace"]] }, { name: "password", label: person ? "New password (leave blank to keep)" : "Password", type: "password", required: !person, autocomplete: "new-password", maxlength: 1024, help: "At least 10 characters. Spaces and Unicode are welcome." }, ...(person ? [{ name: "disabled", label: "Disable this account", type: "checkbox", value: person.disabled }] : []), ...grantsFields(person?.grants)], person ? "Save account" : "Create account", async data => {
      if (data.password) passwordValid(data.password); const grants = parseGrants(data); const body = { displayName: data.displayName, role: data.role, ...(data.password ? { password: data.password } : {}), ...(person ? { disabled: data.disabled, grants } : { username: data.username, ...(grants ? { grants } : {}) }) };
      await mutation(() => api.request(person ? "users/" + key(person.id) : "users", { method: person ? "PATCH" : "POST", body }), person ? "Account updated. Affected sessions have been revoked." : "Account created.");
      if (person?.id === user.id) { await enterSession(await api.request("session")); return; } await renderPeople(active);
    });
    const rows = (result.users || []).map(person => h("tr", {}, h("td", {}, person.displayName || person.username, h("small", { class: "muted" }, " · " + person.username)), h("td", {}, person.role), h("td", {}, person.disabled ? "Disabled" : "Active"), h("td", {}, person.grants == null ? "Role permissions" : (person.grants.read?.length || 0) + " read / " + (person.grants.write?.length || 0) + " write paths"), h("td", {}, h("div", { class: "actions" }, button("Edit", () => edit(person)), button("Delete", async () => { if (!win.confirm("Delete the account “" + person.username + "” and revoke its sessions?")) return; await mutation(() => api.request("users/" + key(person.id), { method: "DELETE" }), "Account deleted."); if (person.id === user.id) showLogin("Sign in with another account."); else await renderPeople(active); }, { class: "danger" })))));
    $("view").replaceChildren(h("section", { class: "card" }, h("div", { class: "spread" }, h("h2", {}, "Accounts and access"), button("Create account", () => edit(), { class: "primary" })), table(["Account", "Role", "Status", "Content access", "Actions"], rows)));
  }
  const downloadTimers = new Map();
  $("login-form").addEventListener("submit", safe(async event => {
    event.preventDefault(); if (operationBusy) return;
    const username = $("login-form").querySelector('[name="username"]').value; const passwordField = $("login-form").querySelector('[name="password"]'); const password = passwordField.value; passwordField.value = "";
    if ((workspace.dirty || formDirty) && !win.confirm("Sign in and discard unsaved changes in this browser? You can download an open draft first.")) return;
    operationBusy = true; $("login-form").querySelector('button[type="submit"]').disabled = true; $("login-error").textContent = "";
    try { const session = await api.request("session", { method: "POST", body: { username, password } }); workspace.clear(); destroyEditor(); formDirty = false; await enterSession(session); }
    finally { operationBusy = false; $("login-form").querySelector('button[type="submit"]').disabled = false; }
  }));
  $("logout").addEventListener("click", safe(async () => {
    if (!await workspace.discardAllowed() || !await allowViewChange()) return;
    await mutation(() => api.request("session", { method: "DELETE" })); workspace.clear(); destroyEditor(); $("view").replaceChildren(); documents = []; renderTree(); showLogin("You have signed out.");
  }));
  $("recover-draft").addEventListener("click", downloadDraft); $("create-document").addEventListener("click", createDocument); $("document-search").addEventListener("input", renderTree);
  doc.querySelectorAll("[data-view]").forEach(node => node.addEventListener("click", safe(() => navigate(node.dataset.view))));
  $("dialog-cancel").addEventListener("click", () => { if (!dialogBusy) $("action-dialog").close(); }); $("action-dialog").addEventListener("cancel", event => { if (dialogBusy) event.preventDefault(); });
  const beforeUnload = event => { if (workspace.dirty || formDirty || operationBusy || workspace.busy) { event.preventDefault(); event.returnValue = ""; } }; win.addEventListener("beforeunload", beforeUnload);
  const colorQuery = win.matchMedia("(prefers-color-scheme: dark)"); const colorChange = () => applyAppearance(settings?.theme); colorQuery.addEventListener("change", colorChange);
  const compactQuery = win.matchMedia("(max-width: 800px)"); const navigation = doc.querySelector(".workspace-navigation"); let compactOpen = false; const navigationChange = () => { navigation.open = !compactQuery.matches || compactOpen; };
  navigation.addEventListener("toggle", () => { if (compactQuery.matches) compactOpen = navigation.open; }); compactQuery.addEventListener("change", navigationChange); navigationChange();
  const ready = api.request("session").then(enterSession).catch(showError).finally(() => { if (!user) $("login-progress").textContent = $("setup-notice").hidden ? "Sign in with your workspace account." : ""; });
  function dispose() { api.abortAll(); filesLoad?.abort(); viewLoad?.abort(); previewLoad?.abort(); workspace.dispose(); destroyEditor(); colorQuery.removeEventListener("change", colorChange); compactQuery.removeEventListener("change", navigationChange); win.removeEventListener("beforeunload", beforeUnload); for (const [timer, url] of downloadTimers) { clearTimeout(timer); URL.revokeObjectURL(url); } downloadTimers.clear(); }
  win.addEventListener("pagehide", dispose, { once: true });
  return { api, workspace, ready, navigate, openDocument, openSource, dispose, get user() { return user; }, get documents() { return documents; }, get settings() { return settings; } };
}

if (typeof document !== "undefined" && typeof window !== "undefined") boot();
