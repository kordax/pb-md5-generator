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

export class Workspace {
  constructor(api, { confirmDiscard = () => true, changed = () => {} } = {}) {
    this.api = api; this.confirmDiscard = confirmDiscard; this.changed = changed; this.document = null; this.content = ""; this.base = ""; this.conflict = null; this.busy = false; this.load = null; this.sequence = 0;
  }
  get dirty() { return !!this.document && (this.document.isNew || this.content !== this.base); }
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
      this.document = doc; this.content = doc.content; this.base = doc.content; this.conflict = null; return true;
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
    this.content = ""; this.base = ""; this.conflict = null; this.changed(); return true;
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
      this.content = file.content; this.base = file.content; this.conflict = null; return true;
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
    const id = this.document.id; const content = this.content; const revision = this.document.revision; const sequence = this.sequence;
    return this.exclusive(async () => {
      try {
        const result = await this.api.request("documents/" + encodeURIComponent(id), { method: "PUT", body: { content, revision, publish, message, ...(this.document.draftId ? { draftId: this.document.draftId } : {}), ...(this.document.draftRevision ? { draftRevision: this.document.draftRevision } : {}), ...(this.document.baseRevision ? { baseRevision: this.document.baseRevision } : {}) } });
        if (sequence !== this.sequence) return result;
        if (!result?.revision) throw new APIError("The save response did not include a revision. Your draft is still here.");
        const saved = result.document || {};
        this.document = { ...this.document, ...saved, revision: result.revision, content: saved.content ?? content,
          draft: !result.published, draftId: result.published ? "" : result.draft?.id || saved.draftId || "", draftRevision: result.published ? "" : result.draft?.revision || saved.draftRevision || "", baseRevision: result.published ? "" : result.draft?.baseRevision || saved.baseRevision || "" };
        this.base = saved.content ?? content; this.conflict = null;
        return result;
      } catch (error) {
        if (sequence === this.sequence && error.details?.draft) {
          const draft = error.details.draft, saved = error.details.document || {};
          this.document = { ...this.document, ...saved, revision: draft.baseRevision, draft: true, draftId: draft.id, draftRevision: draft.revision, baseRevision: draft.baseRevision };
          this.base = saved.content ?? content;
        }
        if (error.status === 409 || error.code === "conflict") this.conflict = { message: error.message, revision: error.details?.revision || error.details?.currentRevision || "" };
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
  async rebase(revision) {
    if (!this.document) throw new APIError("Open a document first.");
    if (this.document.resource === "file") return this.rebaseSource(revision);
    const id = this.document.id, content = this.content, sequence = this.sequence;
    return this.exclusive(async () => {
      let draftId = this.document.draftId, draftRevision = this.document.draftRevision;
      if (!draftId || !draftRevision) {
        const current = normalizeDocument(await this.api.request("documents/" + encodeURIComponent(id)));
        draftId = current.draftId; draftRevision = current.draftRevision;
      }
      try {
        const result = await this.api.request("documents/" + encodeURIComponent(id) + "/rebase", { method: "POST", body: { content, revision, ...(draftId ? { draftId, draftRevision } : {}) } });
        if (sequence !== this.sequence) return result;
        if (!result?.revision) throw new APIError("The save response did not include a revision. Your text is still here.");
        this.document = { ...this.document, ...result.document, revision: result.revision, content, draft: true, draftId: result.draft?.id || result.document?.draftId || "", draftRevision: result.draft?.revision || result.document?.draftRevision || "", baseRevision: result.draft?.baseRevision || revision };
        this.base = result.document?.content ?? content; this.conflict = null; return result;
      } catch (error) {
        if (error.status === 409) this.conflict = { message: error.message, revision: error.details?.currentRevision || "" };
        throw error;
      }
    });
  }
  async latest() {
    if (!this.document) return null;
    if (this.document.resource === "file") return this.api.source(this.document.path);
    return normalizeDocument(await this.api.request("documents/" + encodeURIComponent(this.document.id) + "?published=true"));
  }
  clear() { this.sequence++; this.load?.abort(); this.load = null; this.document = null; this.content = ""; this.base = ""; this.conflict = null; this.changed(); }
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
  if (doc.kind === "protobuf" || /\.proto$/i.test(doc.path)) return "protobuf";
  if (/\.ya?ml$/i.test(doc.path)) return "yaml";
  if (doc.kind === "openapi" || /\.json$/i.test(doc.path)) return "json";
  return "markdown";
}
export function previewDocument(html, nonce = "") {
  if (typeof html !== "string" || encoder.encode(html).length > 16 << 20) throw new APIError("The preview is too large.", 413);
  const safeNonce = /^[A-Za-z0-9+/_=-]{0,128}$/.test(nonce) ? nonce : "";
  return '<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src \'none\'; script-src \'none\'; style-src \'self\' \'nonce-' + safeNonce + '\'; img-src \'self\' data:; font-src \'self\'; base-uri \'none\'; form-action \'none\'"><style nonce="' + safeNonce + '">body{font:16px/1.7 system-ui,sans-serif;color:#172033;max-width:920px;margin:24px auto;padding:0 20px;overflow-wrap:anywhere}pre{overflow:auto;padding:16px;background:#f1f5f9}img{max-width:100%}table{border-collapse:collapse}td,th{padding:8px;border:1px solid #ccd4df}blockquote{border-left:3px solid #2563eb;padding-left:18px}</style></head><body>' + html + "</body></html>";
}
