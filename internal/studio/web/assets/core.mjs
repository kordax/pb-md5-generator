export const MAX_SOURCE_BYTES = 4 << 20;
export const MAX_UPLOAD_BYTES = 16 << 20;
const MAX_RESPONSE_BYTES = 24 << 20;
const encoder = new TextEncoder();

export class APIError extends Error {
  constructor(message, status = 0, code = "request_failed", details = null) {
    super(message); this.name = "APIError"; this.status = status; this.code = code; this.details = details;
  }
}

export function textWithinLimit(value, maximum = MAX_SOURCE_BYTES) {
  if (typeof value !== "string" || value.length > maximum || encoder.encode(value).length > maximum) throw new APIError("This file is too large to edit here.", 413, "source_too_large");
  return value;
}

export function cleanPath(value) {
  if (typeof value !== "string" || !value || value.length > 2048 || value.startsWith("/") || value.includes("\\") || /[\u0000-\u001f\u007f]/.test(value) || value.split("/").some(part => !part || part === "." || part === "..")) throw new APIError("Enter a relative path without empty segments, . or ...", 400, "invalid_path");
  return value;
}

export function can(user, action, path = "") {
  if (!user || user.disabled) return false;
  if (action === "admin") return user.role === "admin";
  if (action !== "read" && action !== "write") return false;
  if (path !== ".") { try { cleanPath(path); } catch (_) { return false; } }
  if (!["admin", "editor", "viewer"].includes(user.role) || (action === "write" && user.role === "viewer")) return false;
  if (user.grants == null) return true;
  const prefixes = user.grants[action];
  return Array.isArray(prefixes) && prefixes.some(prefix => prefix === "." || path === prefix || path.startsWith(prefix + "/"));
}

export class API {
  constructor(base, fetcher = globalThis.fetch, onExpired = () => {}) {
    this.base = new URL(base); this.fetcher = fetcher; this.csrfToken = ""; this.onExpired = onExpired; this.pending = new Set();
  }
  session(value) { this.csrfToken = value?.csrfToken || ""; }
  abortAll() { for (const controller of this.pending) controller.abort(); }
  async source(path, { draftId = "", signal } = {}) {
    cleanPath(path);
    const result = await this.request("files/content?path=" + encodeURIComponent(path) + (draftId ? "&draftId=" + encodeURIComponent(draftId) : ""), { signal, source: true });
    if (!result || draftId && (!result.baseRevision || result.baseRevision.length > 256 || /[\u0000-\u001f\u007f\"]/.test(result.baseRevision))) throw new APIError("The saved source draft response did not include its base revision.");
    return result;
  }
  async request(path, { method = "GET", body, signal, headers = {}, raw = false, source = false } = {}) {
    if (signal?.aborted) throw new DOMException("Canceled", "AbortError");
    const url = new URL(path, this.base);
    if (url.origin !== this.base.origin || !url.pathname.startsWith(this.base.pathname)) throw new APIError("Invalid workspace request.");
    const unsafe = !["GET", "HEAD"].includes(method);
    if (unsafe && !(method === "POST" && path === "session") && !this.csrfToken) throw new APIError("Sign in again before saving changes.", 401, "session_expired");
    if (this.pending.size >= 8) throw new APIError("Wait for the current requests to finish.", 429, "busy");
    const controller = new AbortController(); this.pending.add(controller);
    const abort = () => controller.abort();
    const timer = setTimeout(abort, 30000);
    if (signal?.aborted) abort(); else signal?.addEventListener("abort", abort, { once: true });
    try {
      const response = await this.fetcher(url, { method, credentials: "same-origin", redirect: "error", signal: controller.signal,
        headers: { Accept: source ? "application/octet-stream" : "application/json", ...(body !== undefined && !raw ? { "Content-Type": "application/json" } : {}), ...(unsafe && this.csrfToken ? { "X-CSRF-Token": this.csrfToken } : {}), ...headers },
        ...(body !== undefined ? { body: raw ? body : JSON.stringify(body) } : {}),
      });
      if (response.status === 204) return null;
      const sourceResponse = source && response.ok, maximum = sourceResponse ? MAX_SOURCE_BYTES : MAX_RESPONSE_BYTES;
      if (Number(response.headers.get("content-length")) > maximum) { await response.body?.cancel(); throw new APIError("The workspace response is too large.", 413); }
      let text = "";
      if (response.body?.getReader) {
        const reader = response.body.getReader(); const chunks = []; let length = 0;
        try {
          while (true) { const { done, value } = await reader.read(); if (done) break; length += value.byteLength; if (length > maximum) { await reader.cancel(); throw new APIError("The workspace response is too large.", 413); } chunks.push(value); }
          const bytes = new Uint8Array(length); let offset = 0; for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
          try { text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: sourceResponse }).decode(bytes); }
          catch (_) { throw new APIError(sourceResponse ? "This file is not valid UTF-8 text. Download it instead." : "The workspace returned an unreadable response.", 415, "not_text"); }
        } finally { reader.releaseLock(); }
      } else if (sourceResponse && response.body) { await response.body.cancel?.(); throw new APIError("Streaming source reading is unavailable. Download the file instead."); }
      else { text = await response.text(); if (encoder.encode(text).length > maximum) throw new APIError("The workspace response is too large.", 413); }
      if (sourceResponse) {
        const revision = /^\"([^\"\r\n\u0000-\u001f]{1,256})\"$/.exec(response.headers.get("etag") || "");
        if (!revision || text.includes("\u0000")) throw new APIError(!revision ? "The source response did not include a valid revision. Your open source is retained." : "This file contains binary data. Download it instead.", 415, "not_text");
        const sourceRevision = response.headers.get("x-source-revision") || "";
        if (sourceRevision && !/^[a-f0-9]{64}$/.test(sourceRevision)) throw new APIError("The source response did not include a valid content revision. Your open source is retained.");
        return { content: textWithinLimit(text), revision: revision[1], baseRevision: response.headers.get("x-base-revision") || "", sourceRevision };
      }
      let result;
      try { result = text ? JSON.parse(text) : null; } catch (_) { throw new APIError("The workspace returned an unreadable response. Please try again.", response.status); }
      if (!response.ok) {
        if (response.status === 401 && path !== "session") this.onExpired();
        throw new APIError(String(result?.error?.message || "The request could not be completed.").slice(0, 2048), response.status, result?.error?.code || "request_failed", result?.error);
      }
      return result;
    } catch (error) {
      if (error instanceof APIError || error?.name === "AbortError") throw error;
      throw new APIError("Cannot reach the workspace. Check your connection and try again.");
    } finally { clearTimeout(timer); signal?.removeEventListener("abort", abort); this.pending.delete(controller); }
  }
}

export function unwrap(value, key) { return value?.[key] ?? value; }
export function normalizeDocument(value) {
  const doc = unwrap(value, "document");
  if (!doc || typeof doc.id !== "string" || typeof doc.revision !== "string") throw new APIError("This document response is incomplete.");
  return { ...doc, content: textWithinLimit(doc.content ?? "") };
}

export function documentProperties(value) {
  return Object.freeze({ title: typeof value?.title === "string" ? value.title : "", kind: value?.kind || "markdown", outputPath: typeof value?.outputPath === "string" ? value.outputPath : "" });
}
export function propertiesEqual(left, right) { return (left?.title || "") === (right?.title || "") && (left?.kind || "markdown") === (right?.kind || "markdown") && (left?.outputPath || "") === (right?.outputPath || ""); }
export function validateDocumentProperties(value) {
  if (!value || typeof value !== "object" || Object.keys(value).some(key => !["title", "kind", "outputPath"].includes(key)) || typeof value.title !== "string" || !value.title.trim() || encoder.encode(value.title).length > 256 || /\p{Cc}/u.test(value.title) || !["markdown", "html", "protobuf", "openapi"].includes(value.kind)) throw new APIError("Enter a title of up to 256 UTF-8 bytes and choose a supported document format.");
  const path = value.outputPath;
  if (typeof path !== "string" || !path || encoder.encode(path).length > 4096 || !/^[a-z0-9/._-]+\.html$/.test(path) || path.startsWith("assets/") || path.split("/").some(part => !part || part.startsWith(".") || part.endsWith(".") || part === "_docs" || /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/.test(part))) throw new APIError("Enter an explicit lowercase ASCII relative .html page URL without reserved folders or filenames.");
  return documentProperties(value);
}
const documentRevision = value => typeof value === "string" && value.length > 0 && value.length <= 256 && !/[\"\u0000-\u001f\u007f]/.test(value);

export class Workspace {
  constructor(api, { confirmDiscard = () => true, changed = () => {} } = {}) {
    this.api = api; this.confirmDiscard = confirmDiscard; this.changed = changed; this.document = null; this.content = ""; this.base = ""; this.properties = null; this.baseProperties = null; this.conflict = null; this.busy = false; this.load = null; this.sequence = 0;
  }
  get dirty() { return !!this.document && (this.document.isNew || this.content !== this.base || this.propertiesDirty); }
  get propertiesDirty() { return !!this.document && this.document.resource !== "file" && !propertiesEqual(this.properties, this.baseProperties); }
  editProperties(values) {
    if (!this.document || this.document.resource === "file") throw new APIError("Open a registered document to edit its properties.");
    if (this.document.canWrite === false) throw new APIError("This document is read only.", 403);
    if (!values || typeof values !== "object" || Object.keys(values).some(key => !["title", "kind", "outputPath"].includes(key) || typeof values[key] !== "string" || values[key].length > 4096)) throw new APIError("Only the title, format and page URL can be edited. The document ID and source path are permanent here.");
    const previous = this.properties || documentProperties(this.document); if (!this.baseProperties) this.baseProperties = previous;
    const next = documentProperties({ ...previous, ...values }); if (propertiesEqual(previous, next)) return;
    this.properties = next; this.changed();
  }
  captureDocument() {
    if (!this.properties) this.properties = documentProperties(this.document); if (!this.baseProperties) this.baseProperties = this.properties;
    return { document: this.document, id: this.document.id, path: this.document.path, sequence: this.sequence, content: this.content, properties: this.properties, propertiesDirty: this.propertiesDirty };
  }
  adoptDocumentResult(result, captured, sentProperties = false) {
    if (captured.sequence !== this.sequence) return;
    if (!result?.revision) throw new APIError("The save response did not include a revision. Your draft is still here.");
    const saved = result.document || {};
    if (saved.id && saved.id !== captured.id || captured.path && saved.path && saved.path !== captured.path) throw new APIError("The save response changed the document identity. Your editor is retained; reload the saved draft explicitly.");
    const publishedRevision = saved.publishedRevision || (result.published ? result.revision : captured.document.publishedRevision || (!captured.document.draft && !captured.document.draftId ? captured.document.revision : ""));
    this.document = { ...this.document, ...(sentProperties ? captured.properties : {}), ...saved, revision: result.revision, publishedRevision, content: saved.content ?? captured.content, draft: !result.published, draftId: result.published ? "" : result.draft?.id || saved.draftId || "", draftRevision: result.published ? "" : result.draft?.revision || saved.draftRevision || "", baseRevision: result.published ? "" : result.draft?.baseRevision || saved.baseRevision || "" };
    this.base = saved.content ?? captured.content; this.baseProperties = documentProperties(this.document);
    if (this.properties === captured.properties && (sentProperties || !captured.propertiesDirty)) this.properties = this.baseProperties;
    this.conflict = null;
  }
  get loading() { return this.load !== null; }
  edit(content) { if (this.document?.resource === "file" && content.includes("\u0000")) throw new APIError("Text source cannot contain binary NUL bytes.", 415, "not_text"); this.content = textWithinLimit(content); this.changed(); }
  async discardAllowed() { return !this.dirty || await this.confirmDiscard(); }
  async open(id, { force = false, draftId = "", draftRevision = "" } = {}) {
    if (this.busy) throw new APIError("Wait for the current save to finish.", 409, "busy");
    if (!force && !await this.discardAllowed()) return false;
    this.load?.abort(); const controller = new AbortController(); const sequence = ++this.sequence; this.load = controller; this.changed();
    try {
      const doc = normalizeDocument(await this.api.request("documents/" + encodeURIComponent(id), { signal: controller.signal }));
      if (sequence !== this.sequence || controller.signal.aborted) return false;
      if (draftId && (doc.draftId !== draftId || draftRevision && doc.draftRevision !== draftRevision)) throw new APIError("This saved document draft changed. Refresh its entry before opening it.", 409, "conflict");
      this.document = doc; this.content = doc.content; this.base = doc.content; this.properties = this.baseProperties = documentProperties(doc); this.conflict = null; return true;
    } catch (error) { if (error?.name === "AbortError" || sequence !== this.sequence) return false; throw error; }
    finally { if (sequence === this.sequence) this.load = null; this.changed(); }
  }
  async createFile(path, { revision, canWrite = true } = {}) {
    cleanPath(path);
    if (typeof revision !== "string" || !revision || revision.length > 512 || /[\"\u0000-\u001f]/.test(revision)) throw new APIError("Refresh the folder before creating a text file.", 409, "conflict");
    if (!canWrite) throw new APIError("You cannot create a file at this path.", 403);
    if (this.busy) throw new APIError("Wait for the current save to finish.", 409, "busy");
    const document = this.document, content = this.content, sequence = this.sequence;
    if (!await this.discardAllowed()) return false;
    if (this.busy || sequence !== this.sequence || document !== this.document || content !== this.content) return false;
    this.load?.abort(); this.load = null; this.sequence++;
    this.document = { id: "file:" + path, resource: "file", path, title: path.split("/").at(-1), revision, content: "", canWrite, isNew: true, draft: false, draftId: "", draftRevision: "", baseRevision: "" };
    this.content = ""; this.base = ""; this.properties = this.baseProperties = null; this.conflict = null; this.changed(); return true;
  }
  async openFile(path, { draftId = "", draftRevision = "", force = false, canWrite = true } = {}) {
    cleanPath(path);
    if (this.busy) throw new APIError("Wait for the current save to finish.", 409, "busy");
    if (!force && !await this.discardAllowed()) return false;
    this.load?.abort(); const controller = new AbortController(), sequence = ++this.sequence; this.load = controller; this.changed();
    try {
      const file = await this.api.source(path, { draftId, signal: controller.signal });
      if (sequence !== this.sequence || controller.signal.aborted) return false;
      if (draftId && draftRevision && file.revision !== draftRevision) throw new APIError("This saved source draft changed. Refresh its entry before opening it.", 409, "conflict");
      const base = draftId ? file.baseRevision : file.revision;
      this.document = { id: "file:" + path, resource: "file", path, title: path.split("/").at(-1), revision: base, content: file.content, canWrite, draft: !!draftId, draftId, draftRevision: draftId ? file.revision : "", baseRevision: draftId ? base : "" };
      this.content = file.content; this.base = file.content; this.properties = this.baseProperties = null; this.conflict = null; return true;
    } catch (error) { if (error?.name === "AbortError" || sequence !== this.sequence) return false; throw error; }
    finally { if (sequence === this.sequence) this.load = null; this.changed(); }
  }
  async saveSource(publish = false, message = "") {
    const document = this.document, content = this.content, sequence = this.sequence;
    const revision = document.draftId ? document.baseRevision : document.revision;
    if (!revision || /[\"\r\n\u0000-\u001f]/.test(revision) || document.draftId && !document.draftRevision) throw new APIError("Refresh this source before saving changes.", 409, "conflict");
    const adopt = result => {
      if (sequence !== this.sequence) return;
      if (!result?.revision || !result.published && (!result.draft?.id || !result.draft.revision || !result.draft.baseRevision)) throw new APIError("The source save response is incomplete. Your text is retained; check Publishing before retrying.");
      const draft = result.published ? null : result.draft;
      this.document = { ...this.document, revision: draft?.baseRevision || result.revision, content, isNew: false, draft: !!draft, draftId: draft?.id || "", draftRevision: draft?.revision || "", baseRevision: draft?.baseRevision || "" };
      this.base = content; this.conflict = null;
    };
    return this.exclusive(async () => {
      try {
        let result = await this.api.request("files/content?path=" + encodeURIComponent(document.path), { method: "PUT", raw: true, body: content, headers: { "Content-Type": "application/octet-stream", "If-Match": '"' + revision + '"', ...(document.draftId ? { "X-Draft-ID": document.draftId, "X-Draft-Revision": document.draftRevision } : {}) } });
        adopt(result);
        if (sequence !== this.sequence) return result;
        if (publish && !result.published) {
          result = await this.api.request("drafts/" + encodeURIComponent(result.draft.id) + "/publish", { method: "POST", body: { revision: result.draft.revision, message, push: false } }); adopt(result);
        }
        return result;
      } catch (error) {
        if (sequence === this.sequence && error.details?.draft) adopt({ revision: error.details.currentRevision || error.details.draft.baseRevision, published: false, draft: error.details.draft });
        if (sequence === this.sequence && (error.status === 409 || error.code === "conflict")) this.conflict = { message: error.message, revision: error.details?.revision || error.details?.currentRevision || "" };
        throw error;
      }
    });
  }
  async exclusive(action) {
    if (this.busy || this.loading) throw new APIError("Wait for the current request to finish.", 409, "busy");
    this.busy = true; this.changed();
    try { return await action(); } finally { this.busy = false; this.changed(); }
  }
  async save(publish = false, message = "") {
    if (!this.document) throw new APIError("Open a document first.");
    if (this.document.resource === "file") return this.saveSource(publish, message);
    const captured = this.captureDocument(), document = captured.document;
    const properties = captured.propertiesDirty ? validateDocumentProperties(captured.properties) : null;
    const propertiesRevision = document.publishedRevision || (!document.draft && !document.draftId ? document.revision : "");
    if (properties && !documentRevision(propertiesRevision)) throw new APIError("Refresh the document or compare the latest properties before saving property changes.", 409, "conflict");
    return this.exclusive(async () => {
      try {
        const result = await this.api.request("documents/" + encodeURIComponent(captured.id), { method: "PUT", body: { content: captured.content, revision: document.revision, publish, message, ...(document.draftId ? { draftId: document.draftId } : {}), ...(document.draftRevision ? { draftRevision: document.draftRevision } : {}), ...(document.baseRevision ? { baseRevision: document.baseRevision } : {}), ...(properties ? { properties, propertiesRevision } : {}) } });
        this.adoptDocumentResult(result, captured, !!properties); return result;
      } catch (error) {
        if (captured.sequence === this.sequence && error.details?.draft) this.adoptDocumentResult({ revision: error.details.draft.baseRevision, published: false, draft: error.details.draft, document: error.details.document }, captured, !!properties);
        if (captured.sequence === this.sequence && (error.status === 409 || error.code === "conflict")) this.conflict = { message: error.message, revision: error.details?.revision || error.details?.currentRevision || "" };
        throw error;
      }
    });
  }
  async rebaseSource(revision) {
    const document = this.document, content = this.content, sequence = this.sequence;
    if (typeof revision !== "string" || !revision || revision.length > 512 || /[\"\u0000-\u001f]/.test(revision) || document.draftId && !document.draftRevision) throw new APIError("Compare the latest published source before saving a merged draft.", 409, "conflict");
    return this.exclusive(async () => {
      try {
        const result = await this.api.request("files/rebase?path=" + encodeURIComponent(document.path), { method: "POST", raw: true, body: content, headers: { "Content-Type": "application/octet-stream", "If-Match": '"' + revision + '"', ...(document.draftId ? { "X-Draft-ID": document.draftId, "X-Draft-Revision": document.draftRevision } : {}) } });
        if (sequence !== this.sequence) return result;
        const draft = result?.draft;
        if (result?.published !== false || result.revision !== revision || !draft?.id || !draft.revision || draft.baseRevision !== revision || draft.path !== document.path || document.draftId && draft.id !== document.draftId) throw new APIError("The merged draft response is incomplete. Your text is retained; check Publishing before retrying.");
        this.document = { ...this.document, revision, content, isNew: false, draft: true, draftId: draft.id, draftRevision: draft.revision, baseRevision: draft.baseRevision };
        this.base = content; this.conflict = null; return result;
      } catch (error) {
        if (sequence === this.sequence && (error.status === 409 || error.code === "conflict")) this.conflict = { message: error.message, revision: error.details?.currentRevision || "" };
        throw error;
      }
    });
  }
  async rebase(revision, { propertiesRevision } = {}) {
    if (!this.document) throw new APIError("Open a document first.");
    if (this.document.resource === "file") return this.rebaseSource(revision);
    const captured = this.captureDocument(), document = captured.document;
    const properties = propertiesRevision === undefined ? null : validateDocumentProperties(captured.properties);
    if (properties && (!documentRevision(propertiesRevision) || propertiesRevision !== revision)) throw new APIError("Compare the current published properties again before rebasing.", 409, "conflict");
    if (!!document.draftId !== !!document.draftRevision) throw new APIError("Reopen the exact saved draft before rebasing.", 409, "conflict");
    return this.exclusive(async () => {
      try {
        const result = await this.api.request("documents/" + encodeURIComponent(captured.id) + "/rebase", { method: "POST", body: { content: captured.content, revision, ...(document.draftId ? { draftId: document.draftId, draftRevision: document.draftRevision } : {}), ...(properties ? { properties, propertiesRevision } : {}) } });
        this.adoptDocumentResult({ ...result, published: false }, captured, !!properties); return result;
      } catch (error) {
        if (captured.sequence === this.sequence && error.status === 409) this.conflict = { message: error.message, revision: error.details?.currentRevision || "" };
        throw error;
      }
    });
  }
  async latest() {
    if (!this.document) return null;
    if (this.document.resource === "file") return this.api.source(this.document.path);
    const document = this.document;
    try { return normalizeDocument(await this.api.request("documents/" + encodeURIComponent(document.id) + "?published=true")); }
    catch (error) {
      if (error.status !== 404) throw error;
      const catalog = await this.api.request("documents?published=true");
      if (!Array.isArray(catalog?.documents) || !documentRevision(catalog.revision) || catalog.documents.some(item => item.id === document.id)) throw new APIError("The publication changed while comparing. Compare the latest version again.", 409, "conflict");
      return { ...document, revision: catalog.revision, publishedRevision: catalog.revision, content: "", unpublished: true };
    }
  }
  clear() { this.sequence++; this.load?.abort(); this.load = null; this.document = null; this.content = ""; this.base = ""; this.properties = this.baseProperties = null; this.conflict = null; this.changed(); }
  dispose() { this.sequence++; this.load?.abort(); this.load = null; }
}

export function mergeSettings(original, changes) {
  const switching = changes?.theme?.id && changes.theme.id !== original?.theme?.id;
  const { builtin: _builtin, revision: _revision, ...theme } = switching ? changes.theme : { ...(original?.theme || {}), ...(changes?.theme || {}) };
  const { sync: _sync, ...result } = { ...original, ...changes, theme };
  return result;
}
export function formatLanguage(doc) {
  if (doc.resource === "file") return "text";
  if (doc.kind === "html") return "html";
  if (doc.kind === "protobuf") return "protobuf";
  if (doc.kind === "openapi") return /\.ya?ml$/i.test(doc.path) ? "yaml" : "json";
  if (doc.kind === "markdown") return "markdown";
  if (/\.proto$/i.test(doc.path)) return "protobuf";
  if (/\.ya?ml$/i.test(doc.path)) return "yaml";
  if (/\.json$/i.test(doc.path)) return "json";
  return "markdown";
}
export function previewDocument(html, nonce = "") {
  if (typeof html !== "string" || encoder.encode(html).length > 16 << 20) throw new APIError("The preview is too large.", 413);
  const safeNonce = /^[A-Za-z0-9+/_=-]{0,128}$/.test(nonce) ? nonce : "";
  return '<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src \'none\'; script-src \'none\'; style-src \'self\' \'nonce-' + safeNonce + '\'; img-src \'self\' data:; font-src \'self\'; base-uri \'none\'; form-action \'none\'"><style nonce="' + safeNonce + '">body{font:16px/1.7 system-ui,sans-serif;color:#172033;max-width:920px;margin:24px auto;padding:0 20px;overflow-wrap:anywhere}pre{overflow:auto;padding:16px;background:#f1f5f9}img{max-width:100%}table{border-collapse:collapse}td,th{padding:8px;border:1px solid #ccd4df}blockquote{border-left:3px solid #2563eb;padding-left:18px}</style></head><body>' + html + "</body></html>";
}
