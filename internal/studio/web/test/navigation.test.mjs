import test from "node:test";
import assert from "node:assert/strict";
import { canManageNavigation, validateNavigationTree, editNavigationTree, loadNavigationSnapshot, renderNavigation, MAX_NAVIGATION_DEPTH, MAX_NAVIGATION_NODES, MAX_NAVIGATION_BYTES } from "../assets/navigation.mjs";

const pages = [{ id: "home" }, { id: "guide" }, { id: "api" }];
const page = pageId => ({ kind: "page", pageId });
const group = (id, children, changes = {}) => ({ kind: "group", id, title: id, children, ...changes });
const tree = items => ({ unlistedPages: "append", items });

test("navigation authorization composes administrator and full-project read/write before any request", async () => {
  let requests = 0;
  for (const user of [null, { role: "viewer" }, { role: "editor" }, { role: "admin", disabled: true }, { role: "admin", grants: { read: ["docs"], write: ["."] } }, { role: "admin", grants: { read: ["."], write: ["docs"] } }]) {
    assert.equal(canManageNavigation(user), false);
    await assert.rejects(renderNavigation({ user, api: { request() { requests++; } } }), error => error.status === 403);
  }
  assert.equal(requests, 0); assert.equal(canManageNavigation({ role: "admin" }), true); assert.equal(canManageNavigation({ role: "admin", grants: { read: ["."], write: ["."] } }), true);
});

test("navigation distinguishes automatic mode from an explicit empty custom tree and accepts inert text", () => {
  assert.equal(validateNavigationTree(null, pages), null);
  const empty = { unlistedPages: "omit", items: [] }; assert.equal(validateNavigationTree(empty, pages), empty);
  const value = tree([group("guides", [{ ...page("guide"), title: "<b>Start here</b>" }], { title: "Guides <script>", description: "Line one\n\tLine two", collapsed: true }), page("home")]);
  assert.equal(validateNavigationTree(value, pages), value);
  for (const invalid of [undefined, {}, { items: null }, { items: [], unlistedPages: "guess" }, { items: [], url: "https://foreign" }, tree([{ kind: "external", url: "https://foreign" }]), tree([{ ...page("home"), url: "javascript:attack()" }])]) assert.throws(() => validateNavigationTree(invalid, pages), { name: "APIError" });
});

test("navigation rejects missing pages, repeated identities, unsafe sections and Unicode controls", () => {
  for (const invalid of [tree([page("missing")]), tree([page("home"), page("home")]), tree([group("same", [page("home")]), group("same", [page("guide")])]), tree([group("Upper", [page("home")])]), tree([group("a".repeat(65), [page("home")])]), tree([group("empty", [])]), tree([group("blank", [page("home")], { title: " " })]), tree([{ ...page("home"), title: "" }]), tree([{ ...page("home"), title: "x\nlabel" }]), tree([{ ...page("home"), title: "x\u0085label" }]), tree([{ ...page("home"), title: "\uD800" }]), tree([group("desc", [page("home")], { description: "forbidden\rcontrol" })]), tree([group("bad", [page("home")], { collapsed: null })])]) assert.throws(() => validateNavigationTree(invalid, pages), { name: "APIError" });
  validateNavigationTree(tree([{ ...page("home"), title: "я".repeat(128) }]), pages);
  assert.throws(() => validateNavigationTree(tree([{ ...page("home"), title: "я".repeat(129) }]), pages), /UTF-8/);
  validateNavigationTree(tree([group("desc", [page("home")], { description: "я".repeat(256) })]), pages);
  assert.throws(() => validateNavigationTree(tree([group("desc", [page("home")], { description: "я".repeat(257) })]), pages), /UTF-8/);
});

test("navigation depth, count and escaped-byte budgets are checked before serialization", () => {
  let node = page("home"); for (let depth = 1; depth < MAX_NAVIGATION_DEPTH; depth++) node = group("level" + depth, [node]);
  validateNavigationTree(tree([node]), pages);
  assert.throws(() => validateNavigationTree(tree([group("too-deep", [node])]), pages), /eight levels/);
  const cycle = group("cycle", []); cycle.children.push(cycle); assert.throws(() => validateNavigationTree(tree([cycle]), pages), /cycle/);
  const catalog = Array.from({ length: MAX_NAVIGATION_NODES / 2 }, (_, index) => ({ id: "p" + index }));
  const items = catalog.map((item, index) => group("g" + index, [page(item.id)]));
  validateNavigationTree(tree(items), catalog);
  assert.throws(() => validateNavigationTree(tree([...items, page("home")]), [...catalog, { id: "home" }]), /4096/);
  const escaped = tree(Array.from({ length: 200 }, (_, index) => group("html" + index, [page("p" + index)], { title: "<".repeat(256) })));
  assert.ok(Buffer.byteLength(JSON.stringify(escaped)) < MAX_NAVIGATION_BYTES, "raw JSON fits, while Go HTML escaping does not");
  assert.throws(() => validateNavigationTree(escaped, catalog), error => error.status === 413);
});

test("keyboard tree operations are immutable and preserve whole section subtrees", () => {
  const input = tree([group("section", []), page("home"), page("guide")]);
  const before = structuredClone(input);
  assert.throws(() => editNavigationTree(null, [], "append", page("home")), /Customize navigation/);
  const nested = editNavigationTree(input, [1], "indent"); assert.deepEqual(input, before); assert.equal(nested.items[0].children[0].pageId, "home");
  const out = editNavigationTree(nested, [0, 0], "outdent"); assert.deepEqual(out, input);
  const up = editNavigationTree(input, [2], "up"); assert.equal(up.items[1].pageId, "guide");
  const down = editNavigationTree(up, [1], "down"); assert.deepEqual(down, input);
  const appended = editNavigationTree(input, [0], "append", page("api")); assert.equal(appended.items[0].children[0].pageId, "api");
  const removed = editNavigationTree(appended, [0], "remove"); assert.deepEqual(removed.items, [page("home"), page("guide")]); assert.deepEqual(input, before);
  for (const [path, action] of [[[], "remove"], [[0], "up"], [[2], "down"], [[0], "indent"], [[2], "indent"], [[1], "outdent"], [[5], "remove"], [[-1], "remove"], [[0], "unknown"]]) assert.throws(() => editNavigationTree(input, path, action), { name: "APIError" });
  assert.throws(() => editNavigationTree(input, [1], "append", page("api")), /selection/);
});

test("tree moves reject cycles and depth overflow without changing the source", () => {
  let nested = page("home"); for (let depth = 1; depth < 8; depth++) nested = group("deep" + depth, [nested]);
  const input = tree([group("target", []), nested]); const before = structuredClone(input);
  assert.throws(() => editNavigationTree(input, [1], "indent"), /eight levels/); assert.deepEqual(input, before);
  const cyclic = group("cycle", []); cyclic.children.push(cyclic);
  assert.throws(() => editNavigationTree(tree([]), [], "append", cyclic), /cycle/);
});

test("navigation snapshots retry mismatched project revisions and stop at cancellation or bounded conflict", async () => {
  const calls = []; let settingsReads = 0;
  const api = { async request(path) { calls.push(path); if (path === "settings") return { revision: ++settingsReads === 1 ? "old" : "same", navigation: {} }; return { revision: "same", documents: [] }; } };
  const result = await loadNavigationSnapshot(api); assert.equal(result.settings.revision, "same"); assert.deepEqual(result.pages, []); assert.equal(calls.length, 4); assert.ok(calls.includes("documents?published=true"));
  const conflict = { async request(path) { return path === "settings" ? { revision: "a", navigation: {} } : { revision: "b", documents: [] }; } };
  await assert.rejects(loadNavigationSnapshot(conflict), error => error.status === 409);
  assert.equal(await loadNavigationSnapshot(api, undefined, () => false), null);
  await assert.rejects(loadNavigationSnapshot({ async request() { return {}; } }), /incomplete/);
});
