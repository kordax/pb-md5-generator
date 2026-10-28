import { APIError, can, mergeSettings } from "./core.mjs";

export const MAX_NAVIGATION_DEPTH = 8;
export const MAX_NAVIGATION_NODES = 4096;
export const MAX_NAVIGATION_BYTES = 256 << 10;
const encoder = new TextEncoder();
const unpairedSurrogate = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/;

export function canManageNavigation(user) { return can(user, "admin") && can(user, "read", ".") && can(user, "write", "."); }

function text(value, limit, required, description = false) {
  if (typeof value !== "string" || unpairedSurrogate.test(value) || encoder.encode(value).length > limit || (required && !value.trim()) || /\p{Cc}/u.test(description ? value.replace(/[\n\t]/g, "") : value)) throw new APIError("Navigation labels must be plain text within their UTF-8 byte limit.", 400);
}
function checkedTree(tree, pages, draft = false) {
  if (tree === null) return null;
  if (!tree || typeof tree !== "object" || !Array.isArray(tree.items) || ![undefined, "append", "omit"].includes(tree.unlistedPages) || Object.keys(tree).some(key => !["items", "unlistedPages"].includes(key))) throw new APIError("Choose a navigation tree with an items array and a supported unlisted-pages policy.", 400);
  const pageIDs = new Set(pages?.map(page => page.id));
  const groups = new Set(), placed = new Set(), active = new WeakSet(); let count = 0;
  const visit = (items, depth) => {
    for (const node of items) {
      if (!node || typeof node !== "object" || active.has(node)) throw new APIError("Navigation cannot contain a cycle or an invalid item.", 400);
      if (depth > MAX_NAVIGATION_DEPTH || ++count > MAX_NAVIGATION_NODES) throw new APIError("Navigation supports at most eight levels and 4096 items.", 400);
      active.add(node);
      if (node.kind === "page") {
        if (Object.keys(node).some(key => !["kind", "pageId", "title"].includes(key)) || typeof node.pageId !== "string" || !/^[a-z0-9][a-z0-9._-]{0,127}$/.test(node.pageId) || placed.has(node.pageId)) throw new APIError("Each page can appear only once in navigation.", 400);
        if (!draft && !pageIDs.has(node.pageId)) throw new APIError("Navigation references a missing published page: " + node.pageId.slice(0, 128), 400);
        if (node.title !== undefined) text(node.title, 256, true);
        placed.add(node.pageId);
      } else if (node.kind === "group") {
        if (Object.keys(node).some(key => !["kind", "id", "title", "description", "collapsed", "children"].includes(key)) || typeof node.id !== "string" || !/^[a-z0-9][a-z0-9_-]{0,63}$/.test(node.id) || groups.has(node.id)) throw new APIError("Use unique lowercase section IDs of up to 64 letters, numbers, underscores or hyphens.", 400);
        groups.add(node.id); text(node.title, 256, true);
        if (node.description !== undefined) text(node.description, 512, false, true);
        if (node.collapsed !== undefined && typeof node.collapsed !== "boolean") throw new APIError("Section collapse preference must be a boolean.", 400);
        if (!Array.isArray(node.children) || (!draft && !node.children.length)) throw new APIError("Add a page to each section or remove the empty section before saving.", 400);
        visit(node.children, depth + 1);
      } else throw new APIError("Navigation items must be pages or sections.", 400);
      active.delete(node);
    }
  };
  visit(tree.items, 1);
  // Match Go's compact JSON size, including its HTML and line-separator escapes.
  const encoded = JSON.stringify(tree).replace(/[<>&\u2028\u2029]/g, value => "\\u" + value.charCodeAt(0).toString(16).padStart(4, "0"));
  if (encoder.encode(encoded).length > MAX_NAVIGATION_BYTES) throw new APIError("The encoded navigation tree exceeds 256 KiB.", 413);
  return tree;
}

export function validateNavigationTree(tree, pages) { return checkedTree(tree, pages); }

function siblingsAt(tree, path) {
  let items = tree.items;
  for (const index of path) {
    if (!Number.isInteger(index) || index < 0 || index >= items.length || items[index].kind !== "group") throw new APIError("This navigation selection is no longer available.", 409);
    items = items[index].children;
  }
  return items;
}
export function editNavigationTree(tree, path, action, node) {
  if (!tree) throw new APIError("Customize navigation before editing its tree.", 400);
  checkedTree(tree, null, true);
  if (!Array.isArray(path) || path.length > MAX_NAVIGATION_DEPTH || path.some(index => !Number.isInteger(index) || index < 0)) throw new APIError("Invalid navigation selection.", 400);
  const result = structuredClone(tree);
  if (action === "append") siblingsAt(result, path).push(structuredClone(node));
  else {
    if (!path.length) throw new APIError("Select a navigation item first.", 400);
    const parent = path.slice(0, -1), index = path.at(-1), siblings = siblingsAt(result, parent);
    if (index >= siblings.length) throw new APIError("This navigation selection is no longer available.", 409);
    if (action === "remove") siblings.splice(index, 1);
    else if (action === "up" || action === "down") {
      const target = index + (action === "up" ? -1 : 1);
      if (target < 0 || target >= siblings.length) throw new APIError("This item cannot move further in that direction.", 400);
      [siblings[index], siblings[target]] = [siblings[target], siblings[index]];
    } else if (action === "indent") {
      if (index === 0 || siblings[index - 1].kind !== "group") throw new APIError("Move this item after a section before nesting it.", 400);
      siblings[index - 1].children.push(siblings.splice(index, 1)[0]);
    } else if (action === "outdent") {
      if (!parent.length) throw new APIError("This item is already at the top level.", 400);
      const grandparents = siblingsAt(result, parent.slice(0, -1));
      grandparents.splice(parent.at(-1) + 1, 0, siblings.splice(index, 1)[0]);
    } else throw new APIError("Unsupported navigation action.", 400);
  }
  checkedTree(result, null, true);
  return result;
}

export async function loadNavigationSnapshot(api, signal, current = () => true) {
  for (let attempt = 0; attempt < 3; attempt++) {
    const [settings, catalog] = await Promise.all([api.request("settings", { signal }), api.request("documents?published=true", { signal })]);
    if (!current()) return null;
    if (!settings?.revision || !settings.navigation || !Array.isArray(catalog?.documents) || !catalog.revision) throw new APIError("The navigation settings or page catalog response is incomplete.");
    if (settings.revision === catalog.revision) return { settings, pages: catalog.documents };
  }
  throw new APIError("The project changed while loading navigation. Your edits are retained; reload when the current publication finishes.", 409, "conflict");
}

export async function renderNavigation({ host, api, user, h, button, field, workspace, mutate, confirm, active, signal, onDirty, onRevision, onSettings, notify }) {
  if (!canManageNavigation(user)) throw new APIError("An administrator with read and write access to the entire project is required.", 403);
  let original = null, pages = [], tree = null, customTree = null, dirty = false, busy = false, sequence = 0, version = 0, latest = null;
  let form = null, treeHost = null, parent = null, pagePicker = null, unlisted = null, flags = [], save = null, automatic = null;
  const error = h("p", { class: "notice error-text", role: "alert", hidden: true });
  const progress = h("p", { class: "muted small", role: "status" });
  const editorHost = h("div");
  const comparison = h("section", { class: "card", hidden: true, "aria-label": "Navigation revision conflict" });
  const review = button("Review latest navigation", () => load(false));
  const reload = button("Reload saved navigation", () => { if (!dirty || confirm("Replace your unsaved navigation edits with the saved settings? Your open document is retained.")) return load(true); });
  host.replaceChildren(h("section", { class: "card" }, h("div", { class: "spread" }, h("h2", {}, "Documentation navigation"), h("div", { class: "actions" }, review, reload)), h("p", {}, "Organize published pages into sections. Page identities stay stable when their source or public path changes."), progress, error), comparison, editorHost);
  function markDirty() { dirty = true; if (active()) onDirty(true); }
  function report(failure) {
    if (!active()) return;
    error.textContent = (failure?.name === "AbortError" ? "The navigation request timed out or was canceled." : String(failure?.message || "Navigation could not be updated.").slice(0, 2048)) + " Your navigation edits and open document are retained.";
    error.hidden = false;
  }
  function controls() {
    if (form) for (const control of form.querySelectorAll("input,select,textarea,button")) control.disabled = busy || control.hasAttribute("data-unavailable");
    review.disabled = busy; reload.disabled = busy;
    for (const control of comparison.querySelectorAll("button")) control.disabled = busy;
  }
  function availableButton(label, action, unavailable, attributes = {}) { return button(label, action, { ...attributes, disabled: unavailable, ...(unavailable ? { "data-unavailable": "" } : {}) }); }
  function change(action) { if (busy) return; try { tree = action(); markDirty(); renderTree(); } catch (failure) { report(failure); } }
  function selectedParent() { try { return JSON.parse(parent.value || "[]"); } catch (_) { return []; } }
  function updateParentLabels() {
    if (!tree) return;
    const labels = new Map();
    const visit = (items, path = [], titles = []) => items.forEach((node, index) => { if (node.kind === "group") { const next = [...path, index], title = [...titles, node.title]; labels.set(JSON.stringify(next), title.join(" / ")); visit(node.children, next, title); } });
    visit(tree.items); for (const option of parent.options) if (labels.has(option.value)) option.textContent = labels.get(option.value);
  }
  function updateActionLabels(tools, title) { for (const action of tools.querySelectorAll('button[aria-label]')) action.setAttribute("aria-label", action.textContent + " " + title); }
  function renderTree() {
    if (!form) return;
    treeHost.replaceChildren();
    automatic.textContent = tree === null ? "Customize navigation" : "Use automatic navigation";
    unlisted.wrapper.hidden = tree === null;
    const used = new Set(), groups = [], pageByID = new Map(pages.map(page => [page.id, page]));
    const walk = (items, path = [], titles = []) => items.forEach((node, index) => { if (node.kind === "page") used.add(node.pageId); else { groups.push({ path: [...path, index], title: [...titles, node.title].join(" / ") }); walk(node.children, [...path, index], [...titles, node.title]); } });
    if (tree) walk(tree.items);
    const previousParent = parent.value;
    parent.replaceChildren(h("option", { value: "[]" }, "Top level"), ...groups.map(group => h("option", { value: JSON.stringify(group.path) }, group.title))); parent.value = [...parent.options].some(option => option.value === previousParent) ? previousParent : "[]";
    const previousPage = pagePicker.value;
    pagePicker.replaceChildren(...pages.filter(page => !used.has(page.id)).map(page => h("option", { value: page.id }, page.title + " · " + page.path))); pagePicker.value = [...pagePicker.options].some(option => option.value === previousPage) ? previousPage : pagePicker.options[0]?.value || "";
    if (tree === null) treeHost.append(h("p", { class: "notice" }, "Automatic navigation uses the published page order. No custom tree is saved."));
    else {
      unlisted.input.value = tree.unlistedPages || "append";
      const list = (items, prefix = []) => h("ol", { class: "navigation-editor-list" }, items.map((node, index) => {
        const path = [...prefix, index], page = pageByID.get(node.pageId), title = node.kind === "group" ? node.title : node.title || page?.title || node.pageId;
        const tools = h("div", { class: "actions" }, availableButton("Move up", () => change(() => editNavigationTree(tree, path, "up")), index === 0, { "aria-label": "Move up " + title }), availableButton("Move down", () => change(() => editNavigationTree(tree, path, "down")), index + 1 === items.length, { "aria-label": "Move down " + title }), availableButton("Indent", () => change(() => editNavigationTree(tree, path, "indent")), index === 0 || items[index - 1].kind !== "group", { "aria-label": "Indent " + title }), availableButton("Outdent", () => change(() => editNavigationTree(tree, path, "outdent")), !prefix.length, { "aria-label": "Outdent " + title }), button("Remove", () => { if (node.kind === "group" && node.children.length && !confirm("Remove this section and its navigation entries? The documents themselves are retained.")) return; change(() => editNavigationTree(tree, path, "remove")); }, { "aria-label": "Remove " + title, class: "danger" }));
        if (node.kind === "page") {
          const alias = field({ name: "navigationPageTitle", label: "Navigation label", value: node.title || "", maxlength: 256, placeholder: page?.title || node.pageId, help: "Leave blank to follow the document title." });
          alias.input.addEventListener("input", () => { if (alias.input.value) node.title = alias.input.value; else delete node.title; updateActionLabels(tools, node.title || page?.title || node.pageId); markDirty(); });
          return h("li", { class: "navigation-editor-item", "data-navigation-page": node.pageId }, h("strong", {}, page?.title || "Missing page: " + node.pageId), h("p", { class: "path muted small" }, node.pageId), alias.wrapper, tools);
        }
        const definitions = [{ name: "navigationGroupId", label: "Section ID", value: node.id, maxlength: 64, pattern: "[a-z0-9][a-z0-9_-]{0,63}", required: true }, { name: "navigationGroupTitle", label: "Section title", value: node.title, maxlength: 256, required: true }, { name: "navigationGroupDescription", label: "Section description", type: "textarea", value: node.description || "", maxlength: 512 }, { name: "navigationGroupCollapsed", label: "Collapse this section by default", type: "checkbox", value: node.collapsed || false }];
        const fields = definitions.map(field);
        for (const [index, property] of ["id", "title", "description", "collapsed"].entries()) fields[index].input.addEventListener(property === "collapsed" ? "change" : "input", () => { node[property] = fields[index].type === "checkbox" ? fields[index].input.checked : fields[index].input.value; if (property === "title") { updateParentLabels(); updateActionLabels(tools, node.title); } markDirty(); });
        return h("li", { class: "navigation-editor-item", "data-navigation-group": node.id }, h("fieldset", {}, h("legend", {}, "Section"), h("div", { class: "settings-grid" }, fields.map(item => item.wrapper)), tools, !node.children.length ? h("p", { class: "notice small" }, "Add a page or subsection here before saving, or remove this empty section.") : null, list(node.children, path)));
      }));
      treeHost.append(tree.items.length ? list(tree.items) : h("p", { class: "notice" }, "The custom tree is empty. Unlisted-page policy determines whether the remaining pages appear."));
    }
    const customControls = form.querySelector('[data-navigation-add]'); customControls.hidden = tree === null; controls();
  }
  function renderForm() {
    flags = [field({ name: "showTOC", label: "Show table of contents", type: "checkbox", value: original.navigation.showTOC }), field({ name: "showPreviousNext", label: "Show previous and next pages", type: "checkbox", value: original.navigation.showPreviousNext }), field({ name: "stickyHeader", label: "Keep header visible", type: "checkbox", value: original.navigation.stickyHeader })];
    parent = h("select", { name: "navigationParent", "aria-label": "Parent section" }); pagePicker = h("select", { name: "navigationPage", "aria-label": "Published page" });
    unlisted = field({ name: "navigationUnlisted", label: "Pages not placed in the tree", value: tree?.unlistedPages || "append", options: [["append", "Append after the custom tree"], ["omit", "Leave out of navigation"]], help: "This controls the menu and previous/next links. Page access and search follow account permissions." });
    unlisted.input.addEventListener("change", () => { if (tree) tree.unlistedPages = unlisted.input.value; markDirty(); });
    automatic = button("", () => change(() => { if (tree !== null) { customTree = structuredClone(tree); return null; } return customTree ? structuredClone(customTree) : { unlistedPages: "append", items: pages.map(page => ({ kind: "page", pageId: page.id })) }; }));
    save = h("button", { type: "submit", class: "primary" }, "Save navigation"); treeHost = h("div");
    const add = h("div", { "data-navigation-add": "", class: "navigation-editor-add" }, h("div", { class: "settings-grid" }, h("label", {}, "Parent section", parent), h("label", {}, "Published page", pagePicker)), h("div", { class: "actions" }, button("Add selected page", () => { if (!pagePicker.value) return report(new APIError("All published pages are already placed. Move or remove an entry to reorganize it.")); change(() => editNavigationTree(tree, selectedParent(), "append", { kind: "page", pageId: pagePicker.value })); }), button("Add section", () => {
      const ids = new Set(); const collect = items => items.forEach(node => { if (node.kind === "group") { ids.add(node.id); collect(node.children); } }); collect(tree.items); let index = 1; while (ids.has("section-" + index)) index++;
      change(() => editNavigationTree(tree, selectedParent(), "append", { kind: "group", id: "section-" + index, title: "New section", children: [] }));
    })));
    form = h("form", { class: "card navigation-form" }, h("h2", {}, "Reading and navigation"), h("div", { class: "actions" }, flags.map(item => item.wrapper)), h("div", { class: "actions" }, automatic, save), unlisted.wrapper, add, treeHost);
    form.addEventListener("input", markDirty); form.addEventListener("change", markDirty);
    form.addEventListener("submit", event => { event.preventDefault(); void execute(async () => {
      const navigation = { ...original.navigation, ...Object.fromEntries(flags.map(flag => [flag.name, flag.input.checked])), tree: validateNavigationTree(tree, pages) };
      const result = await api.request("settings", { method: "PUT", body: mergeSettings(original, { navigation }) });
      if (!active()) return;
      if (!result?.revision || !result.navigation) throw new APIError("The navigation save response is incomplete. Your edits are retained.");
      apply({ settings: result, pages });
      notify(result.sync?.error || result.sync?.retryable || result.sync?.state === "pending" ? "Navigation saved locally. " + (result.sync.error || "Git synchronization remains pending.") : "Navigation saved. Your open document and drafts are retained.");
    }); });
    editorHost.replaceChildren(form); renderTree();
  }
  function apply(snapshot) {
    original = snapshot.settings; pages = snapshot.pages; tree = original.navigation.tree == null ? null : structuredClone(validateNavigationTree(original.navigation.tree, pages));
    customTree = tree === null ? null : structuredClone(tree);
    dirty = false; latest = null; comparison.hidden = true; error.hidden = true; onDirty(false); onRevision(original.revision); onSettings?.(original); progress.textContent = "Settings and published page catalog loaded at revision " + original.revision; renderForm();
  }
  async function execute(action) {
    if (busy || !active()) return;
    busy = true; version++; error.hidden = true; controls();
    try { return await mutate(action); } catch (failure) { report(failure); }
    finally { busy = false; if (active()) controls(); }
  }
  async function load(replace) {
    const run = ++sequence, generation = version;
    progress.textContent = "Loading matching settings and published pages…";
    try {
      const snapshot = await loadNavigationSnapshot(api, signal, () => active() && run === sequence && generation === version);
      if (!snapshot || !active() || run !== sequence || generation !== version) return;
      if (!original || replace) return apply(snapshot);
      latest = snapshot; error.hidden = true;
      comparison.hidden = false; comparison.replaceChildren(h("h2", {}, "Review saved navigation"), h("p", {}, "Your edited tree is retained. Compare the saved layout before selecting its revision for your next save."), h("pre", { class: "storage-comparison" }, JSON.stringify({ revision: latest.settings.revision, navigation: latest.settings.navigation }, null, 2)), button("Use reviewed navigation revision", () => {
        if (!confirm("Save your edited navigation using the reviewed revision? This may replace layout changes made elsewhere. Your document text is retained.")) return;
        original = latest.settings; pages = latest.pages; latest = null; comparison.hidden = true; markDirty(); progress.textContent = "Reviewed revision selected. Your edited tree has not been saved."; renderTree();
      })); progress.textContent = "Current saved navigation is shown below for review. Your form is unchanged.";
    } catch (failure) { if (active() && run === sequence && generation === version) { progress.textContent = "Navigation could not be loaded. Retry with Reload saved navigation."; report(failure); } }
  }
  await load(true);
}
