import { APIError, can } from "./core.mjs";

const encoder = new TextEncoder();
const STATES = { unknown: "Not checked", ready: "Connection ready", pending: "Push pending", error: "Failed", disabled: "Push disabled" };

export function canManageStorage(user) { return can(user, "admin") && can(user, "read", ".") && can(user, "write", "."); }

export function storageResult(value) {
  const settings = value?.settings;
  if (!settings || !["local", "git"].includes(settings.backend) || typeof settings.revision !== "string" || !settings.revision || typeof value.projectRevision !== "string" || !value.projectRevision || typeof settings.pullRemote !== "string" || !Array.isArray(settings.remotes) || settings.remotes.length > 8 || !Array.isArray(value.remotes) || !value.sync) throw new APIError("The storage response is incomplete. Refresh before changing connections.");
  if (settings.remotes.some(remote => !remote || typeof remote.name !== "string" || typeof remote.url !== "string" || typeof remote.push !== "boolean")) throw new APIError("The storage connection list is incomplete. Refresh before saving.");
  return { settings: { revision: settings.revision, backend: settings.backend, pullRemote: settings.pullRemote, remotes: settings.remotes.map(({ name, url, push }) => ({ name, url, push })) }, projectRevision: value.projectRevision, sync: value.sync, remotes: value.remotes };
}

export function storageRequest(saved, draft) {
  if (saved.settings.backend === "git" && draft.backend !== "git") throw new APIError("Returning from Git to local storage is not supported.", 400);
  if (!["local", "git"].includes(draft.backend)) throw new APIError("Choose a supported storage backend.", 400);
  if (!Array.isArray(draft.remotes) || draft.remotes.length > 8) throw new APIError("At most eight connections can be configured.", 400);
  const names = new Set();
  const remotes = draft.remotes.map(remote => {
    if (typeof remote?.name !== "string" || typeof remote?.url !== "string" || typeof remote?.push !== "boolean" || /[\u0000-\u001f\u007f]/.test(remote.url)) throw new APIError("Each connection needs a name, address without control characters and an explicit push choice.", 400);
    const name = remote.name.trim(), url = remote.url.trim();
    if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(name) || /^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$/i.test(name) || names.has(name.toLowerCase())) throw new APIError("Use unique connection names of up to 64 ASCII letters, numbers, underscores or hyphens, starting with a letter or number. Reserved device names are not allowed.", 400);
    names.add(name.toLowerCase());
    if (!url || encoder.encode(url).length > 2048 || /[\u0000-\u001f\u007f?#]/.test(url) || url.startsWith("-")) throw new APIError("Enter an SSH or local repository address without passwords, query parameters or fragments (up to 2048 bytes).", 400);
    if (/^[A-Za-z][A-Za-z0-9+.-]*:\/\//.test(url)) {
      let parsed; try { parsed = new URL(url); } catch (_) { throw new APIError("Enter a valid SSH or local repository address.", 400); }
      if (!["ssh:", "file:"].includes(parsed.protocol) || parsed.password || (parsed.protocol === "file:" && (parsed.username || parsed.host || !url.startsWith("file:///"))) || (parsed.protocol === "ssh:" && !parsed.hostname)) throw new APIError("Use SSH or file:///absolute/path. Passwords and file URL hosts are not supported.", 400);
    } else if (!url.startsWith("/") && !/^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:.+$/.test(url)) throw new APIError("Use an SSH address such as git@example.com:team/docs.git or an absolute local repository path.", 400);
    return { name, url, push: !!remote.push };
  });
  const pullRemote = draft.pullRemote || "";
  if (pullRemote && !remotes.some(remote => remote.name === pullRemote)) throw new APIError("Choose a configured connection as the pull source.", 400);
  if (draft.backend === "local" && (remotes.length || pullRemote)) throw new APIError("Choose Git storage before adding repository connections.", 400);
  const enrolling = saved.settings.backend === "local" && draft.backend === "git";
  const initialBranch = draft.initialBranch?.trim() || "main";
  if (enrolling && (encoder.encode(initialBranch).length > 128 || /[\u0000-\u001f\u007f]/.test(initialBranch))) throw new APIError("The initial branch name must be at most 128 UTF-8 bytes without control characters.", 400);
  return { revision: saved.settings.revision, baseRevision: saved.projectRevision, backend: draft.backend, ...(enrolling ? { initialBranch } : {}), pullRemote, remotes };
}

export async function renderStorage({ host, api, user, h, button, field, workspace, mutate, confirm, active, signal, onDirty, onRevision, notify }) {
  if (!canManageStorage(user)) throw new APIError("An administrator with read and write access to the entire project is required.", 403);
  let saved = null, observed = null, dirty = false, busy = false, stale = false, loadSequence = 0, stateVersion = 0;
  let form = null, backend = null, initialBranch = null, noPull = null, save = null, add = null, rowsHost = null, rows = [];
  const statuses = new Map();
  const error = h("p", { class: "notice error-text", role: "alert", hidden: true });
  const progress = h("p", { class: "muted small", role: "status" });
  const summary = h("div");
  const configuration = h("div");
  const connections = h("div", { class: "storage-connections" });
  const comparison = h("section", { class: "card", hidden: true, "aria-label": "Storage revision conflict" });
  const refresh = button("Refresh storage status", () => load(false));
  const reload = button("Reload saved settings", async () => { if (dirty && !confirm("Replace your unsaved storage settings with the saved configuration? Your open document is retained.")) return; await load(true); });
  host.replaceChildren(h("section", { class: "card" }, h("div", { class: "spread" }, h("h2", {}, "Current content storage"), h("div", { class: "actions" }, refresh, reload)), summary, progress, error), comparison, configuration, connections);

  function report(failure) {
    if (!active()) return;
    const message = failure?.name === "AbortError" ? "The storage request timed out or was canceled. Your changes are retained. Refresh status before retrying." : failure?.message || "The storage operation failed. Your changes are retained.";
    error.textContent = String(message).slice(0, 2048) + (failure?.status === 409 ? " Your settings and open document are retained. Refresh storage status to review the latest revisions." : "");
    error.hidden = false;
  }
  function markDirty() { dirty = true; if (active()) onDirty(true); }
  function controls() {
    if (form) for (const input of form.querySelectorAll("input, select, button")) input.disabled = busy;
    if (backend && saved?.settings.backend === "git") backend.input.querySelector('option[value="local"]').disabled = true;
    if (save) save.disabled = busy || stale;
    if (add) add.disabled = busy || rows.length >= 8;
    for (const input of connections.querySelectorAll("button")) input.disabled = busy || stale;
    refresh.disabled = busy; reload.disabled = busy;
    for (const input of comparison.querySelectorAll("button")) input.disabled = busy;
    if (initialBranch) initialBranch.wrapper.hidden = !(saved?.settings.backend === "local" && backend?.input.value === "git");
  }
  function renderSummary(result) {
    const sync = result.sync;
    summary.replaceChildren(h("dl", { class: "status-grid" }, [["Source", result.settings.backend === "git" ? "Git repository" : "Local files"], ["Branch", sync.branch || "—"], ["Commit", sync.head || "—"], ["Source changes", sync.dirty ? "Uncommitted changes" : "Clean"], ["Settings revision", result.settings.revision], ["Project revision", result.projectRevision]].map(([label, value]) => h("div", {}, h("dt", {}, label), h("dd", { class: "path" }, value)))), sync.error || sync.lastError ? h("p", { class: "notice" }, sync.error || sync.lastError) : null);
  }
  function renderComparison() {
    comparison.hidden = !stale;
    if (!stale) return;
    comparison.replaceChildren(h("h2", {}, "Storage or content changed elsewhere"), h("p", {}, "Your entered settings and unsaved document are still here. Review the saved configuration before applying your changes to the new revisions."), h("pre", { class: "storage-comparison" }, JSON.stringify({ settings: observed.settings, projectRevision: observed.projectRevision }, null, 2)), button("Use reviewed revisions", () => {
      if (!confirm("Apply your entered connection list using the reviewed revisions? This can replace connections saved by another administrator. Your document text is retained.")) return;
      saved = observed; statuses.clear(); for (const status of observed.remotes) statuses.set(status.name, status);
      stale = false; comparison.hidden = true; progress.textContent = "Reviewed revisions selected. Your entered settings have not been saved."; markDirty(); renderConnections(); controls();
    }));
  }
  function setPull(selected) { noPull.checked = selected === null; for (const row of rows) row.pull.checked = row === selected; }
  function appendRemote(remote = { name: "", url: "", push: true }) {
    if (rows.length >= 8) return;
    const row = {};
    row.name = field({ name: "remoteName", label: "Connection name", value: remote.name, required: true, maxlength: 64, pattern: "[A-Za-z0-9][A-Za-z0-9_-]{0,63}", autocomplete: "off" });
    row.url = field({ name: "remoteURL", label: "Repository address", value: remote.url, required: true, maxlength: 2048, placeholder: "git@example.com:team/docs.git", autocomplete: "off", spellcheck: "false", help: "SSH, file:// or a local repository path. Credentials are configured on the server." });
    row.push = field({ name: "remotePush", label: "Push commits to this connection", type: "checkbox", value: remote.push });
    row.pull = h("input", { type: "radio", name: "storagePullSource", checked: saved.settings.pullRemote === remote.name && !!remote.name });
    row.pull.addEventListener("change", () => { setPull(row); markDirty(); });
    row.node = h("fieldset", { class: "storage-remote", "data-storage-remote": "" }, h("legend", {}, "Repository connection"), h("div", { class: "settings-grid" }, row.name.wrapper, row.url.wrapper), h("div", { class: "actions" }, row.push.wrapper, h("label", {}, row.pull, "Pull updates from this connection"), button("Remove connection", () => { if (row.pull.checked) setPull(null); rows = rows.filter(item => item !== row); row.node.remove(); markDirty(); controls(); })));
    rows.push(row); rowsHost.append(row.node); controls();
  }
  function renderForm() {
    rows = [];
    backend = field({ name: "storageBackend", label: "Storage backend", value: saved.settings.backend, options: [["local", "Local files"], ["git", "Git repository"]], help: "Git tracks the current documentation in place. Returning from Git to local storage is not supported." });
    initialBranch = field({ name: "initialBranch", label: "Initial branch", value: "main", maxlength: 128, help: "Used only when enabling Git. The default is main." });
    noPull = h("input", { type: "radio", name: "storagePullSource", checked: !saved.settings.pullRemote }); noPull.addEventListener("change", () => setPull(null));
    rowsHost = h("div");
    add = button("Add connection", () => { appendRemote(); markDirty(); });
    save = h("button", { type: "submit", class: "primary" }, "Save storage settings");
    form = h("form", { class: "card storage-form" }, h("h2", {}, "Storage configuration"), h("div", { class: "settings-grid" }, backend.wrapper, initialBranch.wrapper), h("p", { class: "notice" }, "Enabling Git changes project history and its revision. Document text, the published site and saved drafts are retained. Existing drafts may need an explicit rebase before publication."), h("p", { class: "muted small" }, "SSH uses the private key and known_hosts mounted on the server. Keys and passwords are never entered or returned here. Saving settings does not contact remote repositories."), h("label", {}, noPull, "No pull source (local commits and push only)"), rowsHost, h("div", { class: "actions" }, add, save));
    form.addEventListener("input", markDirty); form.addEventListener("change", () => { markDirty(); controls(); });
    for (const remote of saved.settings.remotes) appendRemote(remote);
    form.addEventListener("submit", event => { event.preventDefault(); void execute(async () => {
      if (stale) throw new APIError("Review the current storage revisions before saving your retained settings.", 409);
      const draft = { backend: backend.input.value, initialBranch: initialBranch.input.value, pullRemote: rows.find(row => row.pull.checked)?.name.input.value.trim() || "", remotes: rows.map(row => ({ name: row.name.input.value, url: row.url.input.value, push: row.push.input.checked })) };
      const body = storageRequest(saved, draft);
      const enrolling = saved.settings.backend === "local" && body.backend === "git";
      if (enrolling && workspace.dirty && !confirm("Enabling Git changes history and the project revision. Your unsaved document text and saved drafts will be retained and may need review or rebase before saving. Continue?")) return;
      if (!active()) return;
      const result = storageResult(await api.request("storage/configuration", { method: "PUT", body }));
      if (!active()) return;
      apply(result); onRevision(result.projectRevision);
      notify(enrolling ? "Git enabled. Document text and drafts were retained. Review existing drafts and their base revisions before publishing." : "Storage settings saved. Your open document is retained.");
    }); });
    configuration.replaceChildren(form); controls();
  }
  function renderConnections() {
    if (!saved) return;
    const cards = saved.settings.remotes.map(remote => {
      const status = statuses.get(remote.name) || { name: remote.name, state: remote.push ? "unknown" : "disabled" };
      return h("section", { class: "card", "data-remote-status": remote.name }, h("div", { class: "spread" }, h("h3", {}, remote.name), h("span", { class: "badge" + (status.state === "error" ? " warning" : status.state === "ready" ? " success" : "") }, STATES[status.state] || "Not checked")), h("p", { class: "path" }, remote.url), h("p", { class: "muted small" }, saved.settings.pullRemote === remote.name ? "Pull source" : "Push-only or inactive connection", remote.push ? " · Push enabled" : " · Push disabled"), h("dl", { class: "status-grid" }, [["Branch", status.branch || "—"], ["Current commit", status.head || "—"], ["Last pushed commit", status.lastPushedHead || "—"]].map(([label, value]) => h("div", {}, h("dt", {}, label), h("dd", { class: "path" }, value)))), status.error ? h("p", { class: "notice", role: "status" }, String(status.error).slice(0, 2048)) : null, h("div", { class: "actions" }, button("Check connection", () => attempt(remote, "check"), { "aria-label": "Check connection " + remote.name }), remote.push ? button(status.retryable || status.state === "error" ? "Retry push" : "Push commits", () => attempt(remote, "push"), { "aria-label": "Push commits to " + remote.name }) : null));
    });
    connections.replaceChildren(h("h2", {}, "Saved connections"), h("p", { class: "muted small" }, "Checks and pushes run only when requested. Each push target can be retried independently. These actions use saved settings, including when the form has unsaved edits."), ...cards, !cards.length ? h("p", { class: "card muted" }, "No connections configured. Git can track local history without a remote.") : null); controls();
  }
  function apply(result) {
    saved = result; observed = result; dirty = false; stale = false; statuses.clear();
    for (const status of result.remotes) statuses.set(status.name, status);
    onDirty(false); comparison.hidden = true; renderSummary(result); renderForm(); renderConnections(); progress.textContent = "Storage settings and content status loaded together.";
  }
  async function load(replace) {
    const sequence = ++loadSequence, version = stateVersion;
    progress.textContent = "Loading storage status…";
    try {
      const result = storageResult(await api.request("storage/configuration", { signal }));
      if (!active() || sequence !== loadSequence || version !== stateVersion) return;
      error.hidden = true; observed = result; renderSummary(result); onRevision(result.projectRevision);
      if (!saved || replace) apply(result);
      else {
        stale = result.settings.revision !== saved.settings.revision || result.projectRevision !== saved.projectRevision;
        if (!stale) { for (const status of result.remotes) statuses.set(status.name, status); renderConnections(); }
        progress.textContent = stale ? "Saved revisions changed. Review the current configuration below; your edits are retained." : "Storage status refreshed. Your entered settings are retained.";
        renderComparison(); controls();
      }
    } catch (failure) { if (active() && sequence === loadSequence && version === stateVersion) { progress.textContent = "Storage status could not be loaded. Retry with Refresh storage status."; report(failure); } }
  }
  async function execute(action) {
    if (busy || !active()) return;
    busy = true; stateVersion++; error.hidden = true; controls();
    try { return await mutate(action); } catch (failure) { report(failure); }
    finally { busy = false; if (active()) controls(); }
  }
  async function attempt(remote, action) {
    return execute(async () => {
      let result;
      try { result = await api.request("storage/remotes/" + encodeURIComponent(remote.name) + "/" + action, { method: "POST", body: { revision: saved.settings.revision, ...(action === "push" ? { baseRevision: saved.projectRevision } : {}) } }); }
      catch (failure) {
        if (active() && ![401, 403, 409].includes(failure.status)) { statuses.set(remote.name, { ...statuses.get(remote.name), name: remote.name, state: "error", retryable: true, error: failure?.name === "AbortError" ? "The request timed out or was canceled. Check this connection's status before retrying." : String(failure.message).slice(0, 2048) }); renderConnections(); }
        throw failure;
      }
      if (!active()) return;
      if (!result || result.name !== remote.name || !Object.hasOwn(STATES, result.state)) throw new APIError("The connection response is incomplete. Refresh its status before retrying.");
      statuses.set(remote.name, result); renderConnections();
      notify(result.state === "error" || result.error ? "Connection “" + remote.name + "” failed. " + (result.error || "Retry this connection when it is available.") : action === "push" ? result.state === "ready" && !result.retryable ? "Push to “" + remote.name + "” completed." : "Push to “" + remote.name + "” remains pending. " + STATES[result.state] + "." : "Connection “" + remote.name + "” checked. " + STATES[result.state] + ".");
    });
  }
  await load(true);
}
