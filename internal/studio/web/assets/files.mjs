import { APIError, can, cleanPath } from "./core.mjs";

export const LITERAL_CODE_EXTENSIONS = ["go", "js", "ts", "sh", "sql", "css", "xml"];
export const TEXT_EXTENSIONS = ["md", "markdown", "proto", "json", "yaml", "yml", "html", "htm", "txt", ...LITERAL_CODE_EXTENSIONS];
export const UPLOAD_ACCEPT = ["png", "jpg", "jpeg", "gif", "webp", "avif", "ico", "pdf", ...TEXT_EXTENSIONS].map(extension => "." + extension).join(",");
export function literalCodePath(path) { return LITERAL_CODE_EXTENSIONS.includes(path.split(".").at(-1)?.toLowerCase()); }
export function textFilePath(path) { return TEXT_EXTENSIONS.includes(path.split(".").at(-1)?.toLowerCase()); }
export function insideFolder(path, folder) { return typeof path === "string" && (path === folder || path.startsWith(folder + "/")); }

export function newTextFilePath(name, folder, files = [], drafts = []) {
  if (typeof name !== "string" || !name || /[\\/]/.test(name)) throw new APIError("Enter a filename in the current folder.");
  const path = cleanPath(folder === "." ? name : folder + "/" + name);
  if (!textFilePath(path)) throw new APIError("Choose a supported text file extension.", 415);
  const folded = path.toLowerCase(), overlaps = target => typeof target === "string" && (folded === target.toLowerCase() || folded.startsWith(target.toLowerCase() + "/") || target.toLowerCase().startsWith(folded + "/"));
  if (files.some(item => overlaps(item.path))) throw new APIError("A published file or folder already uses this name. Open it from Files instead.", 409, "conflict");
  if (drafts.some(item => overlaps(item.path) || overlaps(item.to))) throw new APIError("A saved change already targets this path. Open or discard it from Publishing first.", 409, "conflict");
  return path;
}

export function sourceHistorySnapshot(value, path) {
  cleanPath(path);
  const token = input => typeof input === "string" && input.length > 0 && input.length <= 512 && !/[\"\u0000-\u001f\u007f]/.test(input);
  if (value?.path !== path || !token(value.revision) || !Array.isArray(value.revisions) || value.revisions.length > 256) throw new APIError("This file history response is incomplete. Refresh before restoring a version.");
  const ids = new Set();
  const revisions = value.revisions.map(item => {
    if (!token(item?.id) || ids.has(item.id) || item.path !== path || !token(item.revision) || typeof item.author !== "string" || item.author.length > 512 || typeof item.createdAt !== "string" || item.createdAt.length > 64 || !Number.isSafeInteger(item.size) || item.size < 0 || typeof item.deleted !== "boolean" || typeof item.restorable !== "boolean" || item.deleted && item.restorable) throw new APIError("This file history response is incomplete. Refresh before restoring a version.");
    ids.add(item.id);
    return { id: item.id, path, revision: item.revision, author: item.author, createdAt: item.createdAt, size: item.size, deleted: item.deleted, restorable: item.restorable };
  });
  return { path, revision: value.revision, revisions };
}

export function deleteTreePlan(value, folder, revision) {
  cleanPath(folder);
  if (!value || value.path !== folder || value.revision !== revision || typeof value.planRevision !== "string" || !value.planRevision || value.planRevision.length > 256 || /[\u0000-\u001f\u007f]/.test(value.planRevision) || !Array.isArray(value.entries) || value.entries.length > 100 || typeof value.truncated !== "boolean") throw new APIError("The folder deletion plan is incomplete. Refresh the folder and review it again.");
  for (const property of ["fileCount", "directoryCount", "documentCount", "totalBytes"]) if (!Number.isSafeInteger(value[property]) || value[property] < 0) throw new APIError("The folder deletion counts are invalid.");
  const total = value.fileCount + value.directoryCount;
  if (!total || total > 1024 || value.fileCount > 127 || value.documentCount > value.fileCount || value.totalBytes > (64 << 20) || value.entries.length !== Math.min(total, 100) || value.truncated !== (total > 100)) throw new APIError("This folder is outside the supported deletion limits.");
  const seen = new Set();
  for (const entry of value.entries) {
    if (!entry || typeof entry.path !== "string") throw new APIError("The folder deletion inventory is invalid.");
    cleanPath(entry.path);
    if (!insideFolder(entry.path, folder) || seen.has(entry.path) || typeof entry.directory !== "boolean" || !Number.isSafeInteger(entry.size) || entry.size < 0) throw new APIError("The folder deletion inventory is invalid.");
    seen.add(entry.path);
  }
  return value;
}

export async function reviewFolderDeletion({ host, folder, revision, user, api, h, button, workspace, mutate, confirm, active, signal, refresh, returnToEditor, downloadSource, onStaged }) {
  cleanPath(folder);
  if (!can(user, "read", folder) || !can(user, "write", folder)) throw new APIError("Read and write access to the whole folder is required.", 403);
  let plan = null, stage = null, busy = false, closed = false;
  const live = () => active() && !closed;
  const preview = new AbortController(), abort = () => preview.abort();
  if (signal?.aborted) abort(); else signal?.addEventListener("abort", abort, { once: true });
  const cancel = button("Cancel deletion review", () => { if (!busy) { closed = true; preview.abort(); host.replaceChildren(); } });
  const error = h("p", { class: "notice error-text", role: "alert", hidden: true });
  const status = h("p", { role: "status", class: "muted" }, "Preparing a deletion plan…");
  const content = h("div");
  const heading = h("h2", { tabindex: "-1" }, "Review folder deletion");
  host.replaceChildren(h("section", { class: "card folder-delete-review", "aria-label": "Folder deletion review" }, h("div", { class: "spread" }, heading, cancel), status, error, content));
  function report(failure) {
    if (!live()) return;
    error.hidden = false; error.textContent = (failure?.name === "AbortError" ? "The deletion request timed out or was canceled. Check Publishing before trying again." : String(failure?.message || "The deletion plan could not be prepared.").slice(0, 2048)) + " Your open source and saved drafts are retained.";
  }
  function controls() { cancel.disabled = busy; for (const control of content.querySelectorAll("button")) control.disabled = busy; if (stage) stage.disabled = busy || !plan; }
  try {
    const result = await api.request("files/delete-preview", { method: "POST", body: { path: folder, revision }, signal: preview.signal });
    if (!live()) return;
    plan = deleteTreePlan(result, folder, revision);
    const rows = plan.entries.map(entry => h("tr", {}, h("td", { class: "path" }, entry.path), h("td", {}, entry.directory ? "Folder" : "File"), h("td", {}, entry.directory ? "—" : entry.size.toLocaleString() + " bytes")));
    const openPath = workspace.document?.path;
    const affected = insideFolder(openPath, folder);
    const sourceNotice = affected ? h("div", { class: "notice" }, h("p", {}, "Your open source “" + openPath + "” is inside this folder." + (workspace.dirty ? " It has unsaved changes." : "") + " Its text stays in the editor. Staging this deletion does not remove published files."), h("div", { class: "actions" }, button("Return to editor", returnToEditor), button("Download open source", downloadSource))) : null;
    stage = button("Stage folder deletion", async () => {
      if (busy || !live() || !plan) return;
      const openAffected = insideFolder(workspace.document?.path, folder);
      const message = "Create a deletion draft for “" + folder + "” containing " + plan.fileCount + " file(s) and " + plan.directoryCount + " folder(s)? " + (plan.truncated ? "The preview lists only the first " + plan.entries.length + " entries; the draft covers the whole folder. " : "") + "Published files change only after you publish this draft." + (openAffected ? " Your open source text will be retained." : "");
      if (!confirm(message)) return;
      busy = true; controls(); error.hidden = true;
      try {
        const saved = await mutate(() => api.request("files/delete-tree", { method: "POST", body: { path: plan.path, revision: plan.revision, planRevision: plan.planRevision } }));
        if (!live()) return;
        if (saved?.published !== false || !saved?.draft?.id || !saved.draft.revision) throw new APIError("The server did not confirm a staged deletion. Check Publishing before trying again.");
        await onStaged(saved, plan);
      } catch (failure) { if (live()) { plan = null; status.textContent = failure?.status === 409 ? "This plan is no longer current. Refresh the folder listing and review again." : "Deletion was not confirmed. Check Publishing before reviewing another plan."; } report(failure); }
      finally { busy = false; if (live()) controls(); }
    }, { class: "danger" });
    status.textContent = "Verified plan: " + plan.fileCount + " file(s), " + plan.directoryCount + " folder(s), " + plan.documentCount + " document(s), " + plan.totalBytes.toLocaleString() + " bytes.";
    content.replaceChildren(h("p", {}, "Folder: ", h("strong", { class: "path" }, folder)), h("p", { class: "notice" }, "This action always saves a separate deletion draft. Automatic publishing does not apply. Review and publish the draft from Publishing when ready."), sourceNotice, plan.truncated ? h("p", { class: "notice", role: "status" }, "Showing the first " + plan.entries.length + " of " + (plan.fileCount + plan.directoryCount) + " entries. All entries were checked by the server; staging covers the full folder.") : null, h("div", { class: "table-scroll", tabindex: "0", "aria-label": "Files included in deletion" }, h("table", {}, h("thead", {}, h("tr", {}, ["Source", "Type", "Size"].map(label => h("th", { scope: "col" }, label)))), h("tbody", {}, rows))), h("div", { class: "actions" }, stage, button("Refresh folder listing", refresh)));
    heading.focus?.();
  } catch (failure) {
    report(failure);
    if (live()) { status.textContent = "No deletion was staged."; content.replaceChildren(button("Refresh folder listing", refresh)); }
  } finally { signal?.removeEventListener("abort", abort); }
}
