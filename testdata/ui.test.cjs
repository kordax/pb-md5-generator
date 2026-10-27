const test = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { resolve } = require("node:path");
const vm = require("node:vm");

const source = readFileSync(resolve(__dirname, "../site/_docs/site.js"), "utf8");

// A small DOM boundary checks enhancement behavior without a browser or npm
// dependencies. Visual layout, CSS/CSP enforcement and browser permissions still
// require browser tests; these tests do not claim to replace them.
function reader(options = {}) {
  let focused;
  class Element {
    constructor(tag = "div") {
      this.tagName = tag;
      this.children = [];
      this.events = new Map();
      this.dataset = {};
      this.hidden = false;
      this.textContent = "";
      this.value = "";
    }
    addEventListener(name, callback) {
      const callbacks = this.events.get(name) || [];
      callbacks.push(callback);
      this.events.set(name, callbacks);
    }
    async emit(name, extra = {}) {
      const event = { type: name, preventDefault() { this.prevented = true; }, ...extra };
      for (const callback of this.events.get(name) || []) await callback(event);
      return event;
    }
    dispatchEvent(event) {
      for (const callback of this.events.get(event.type) || []) callback(event);
    }
    append(...children) {
      for (const child of children) { this.children.push(child); child.parentElement = this; }
    }
    prepend(child) { this.children.unshift(child); child.parentElement = this; }
    replaceChildren(...children) { this.children = []; this.append(...children); }
    querySelector(tag) {
      for (const child of this.children) {
        if (child.tagName === tag) return child;
        const found = child.querySelector(tag);
        if (found) return found;
      }
      return null;
    }
    focus() { focused = this; }
    set innerHTML(_) { throw new Error("Untrusted content must not be rendered as HTML"); }
  }
  const ids = new Map(["theme", "search", "search-status", "results", "search-box"].map(name => ["_docs-" + name, new Element()]));
  ids.get("_docs-search-box").hidden = true;
  const code = new Element("code");
  code.textContent = "console.log('<example>');";
  const pre = new Element("pre");
  pre.append(code);
  const document = {
    currentScript: { dataset: { search: "../_docs/search.json" } },
    baseURI: "https://example.test/team/product/guides/page.html",
    documentElement: new Element("html"),
    getElementById: id => ids.get(id),
    querySelectorAll: () => [code],
    createElement: name => new Element(name),
  };
  const pending = new Map();
  const requests = [];
  const copied = [];
  let sequence = 0;
  const context = {
    document,
    URL,
    Event: class { constructor(type) { this.type = type; } },
    matchMedia: () => ({ matches: options.dark || false }),
    localStorage: {
      getItem() { if (options.storageError) throw new Error("Storage disabled"); return options.theme; },
      setItem(_, value) { if (options.storageError) throw new Error("Storage disabled"); options.theme = value; },
    },
    navigator: { clipboard: { async writeText(value) { if (options.copyError) throw new Error("Denied"); copied.push(value); } } },
    window: { isSecureContext: options.secure !== false },
    fetch(url) {
      requests.push(url.href);
      return options.fetch ? options.fetch(url) : Promise.resolve({ ok: true, json: async () => options.entries || [] });
    },
    setTimeout(callback) { const id = ++sequence; pending.set(id, callback); return id; },
    clearTimeout(id) { pending.delete(id); },
  };
  vm.runInNewContext(source, context, { timeout: 1000 });
  return {
    ids, document, requests, copied, code, pre,
    focused: () => focused,
    async tick() {
      const callbacks = [...pending.values()];
      pending.clear();
      for (const callback of callbacks) await callback();
    },
    async search(value) { const input = ids.get("_docs-search"); input.value = value; await input.emit("input"); },
  };
}

test("reading is idle and search is lazy", async () => {
  const app = reader({ storageError: true, secure: false });
  assert.equal(app.requests.length, 0);
  assert.equal(app.ids.get("_docs-search-box").hidden, false);
  assert.equal(app.pre.children.length, 1, "insecure contexts have no unusable copy control");
  await app.search("   ");
  await app.tick();
  assert.equal(app.requests.length, 0);
});

test("search resolves subpath URLs and renders text safely", async () => {
  const title = '<img src=x onerror="alert(1)"> API';
  const app = reader({ entries: Array.from({ length: 12 }, (_, i) => ({ title, url: "reference/api-" + i + ".html", text: "API examples and usage" })) });
  await app.search("API");
  await app.tick();
  assert.deepEqual(app.requests, ["https://example.test/team/product/_docs/search.json"]);
  const results = app.ids.get("_docs-results");
  assert.equal(results.children.length, 10);
  const first = results.querySelector("a");
  assert.equal(first.href, "https://example.test/team/product/reference/api-0.html");
  assert.equal(first.textContent, title);
  assert.match(app.ids.get("_docs-search-status").textContent, /12 page\(s\).*first 10/);
  const event = await app.ids.get("_docs-search").emit("keydown", { key: "ArrowDown" });
  assert.equal(event.prevented, true);
  assert.equal(app.focused(), first);
  await app.ids.get("_docs-search").emit("keydown", { key: "Escape" });
  assert.equal(results.children.length, 0);
  assert.equal(app.ids.get("_docs-search-status").textContent, "");
});

test("stale asynchronous search cannot replace newer input", async () => {
  let finish;
  const response = new Promise(resolve => { finish = resolve; });
  const app = reader({ fetch: () => response });
  await app.search("alpha");
  const first = app.tick();
  await app.search("beta");
  finish({ ok: true, json: async () => [{ title: "Beta", url: "beta.html", text: "beta" }] });
  await first;
  assert.equal(app.ids.get("_docs-results").children.length, 0);
  await app.tick();
  assert.equal(app.ids.get("_docs-results").querySelector("a").textContent, "Beta");
  assert.equal(app.requests.length, 1, "concurrent queries share the one index request");
});

test("failed index requests can be retried", async () => {
  let attempts = 0;
  const app = reader({ fetch: async () => {
    if (++attempts === 1) return { ok: false };
    return { ok: true, json: async () => [{ title: "Recovered", url: "index.html", text: "recovered" }] };
  } });
  await app.search("recovered");
  await app.tick();
  assert.match(app.ids.get("_docs-search-status").textContent, /Search unavailable/);
  await app.search("recovered");
  await app.tick();
  assert.equal(app.ids.get("_docs-results").querySelector("a").textContent, "Recovered");
  assert.equal(attempts, 2);
});

test("theme works when storage is unavailable", async () => {
  const app = reader({ storageError: true, dark: true });
  assert.equal(app.ids.get("_docs-theme").textContent, "Light theme");
  await app.ids.get("_docs-theme").emit("click");
  assert.equal(app.document.documentElement.dataset.theme, "light");
  assert.equal(app.ids.get("_docs-theme").textContent, "Dark theme");
});

test("copy uses the original code text and reports denied permission", async () => {
  const app = reader();
  const button = app.pre.children[0];
  await button.emit("click");
  assert.deepEqual(app.copied, [app.code.textContent]);
  assert.equal(button.textContent, "Copied");
  const denied = reader({ copyError: true });
  await denied.pre.children[0].emit("click");
  assert.equal(denied.pre.children[0].textContent, "Copy unavailable");
});
