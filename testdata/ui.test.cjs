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
  let scrolled;
  class Element {
    constructor(tag = "div") {
      this.tagName = tag;
      this.children = [];
      this.events = new Map();
      this.dataset = {};
      this.hidden = false;
      this.textContent = "";
      this.value = "";
      this.id = "";
      this.className = "";
      this.attributes = new Map();
      this.classList = {
        add: value => { this.className = [...new Set(this.className.split(/\s+/).filter(Boolean).concat(value))].join(" "); },
        contains: value => this.className.split(/\s+/).includes(value),
      };
    }
    setAttribute(name, value) {
      this.attributes.set(name, String(value));
      if (name === "id") { this.id = String(value); ids.set(this.id, this); }
    }
    getAttribute(name) { return this.attributes.has(name) ? this.attributes.get(name) : null; }
    hasAttribute(name) { return this.attributes.has(name); }
    contains(element) { return this === element || this.children.some(child => child.contains(element)); }
    closest(selector) {
      if (selector !== "a[href^='#']") throw new Error("Unexpected selector: " + selector);
      if (this.tagName === "a" && (this.getAttribute("href") || "").startsWith("#")) return this;
      return this.parentElement ? this.parentElement.closest(selector) : null;
    }
    scrollIntoView() { scrolled = this; }
    addEventListener(name, callback) {
      const callbacks = this.events.get(name) || [];
      callbacks.push(callback);
      this.events.set(name, callbacks);
    }
    async emit(name, extra = {}) {
      const event = { type: name, target: this, defaultPrevented: false, preventDefault() { this.prevented = true; this.defaultPrevented = true; }, ...extra };
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
  const groups = [];
  function element(tag, attributes = {}) {
    const node = new Element(tag);
    for (const [name, value] of Object.entries(attributes)) node.setAttribute(name, value);
    return node;
  }
  function tabGroup(name, labels) {
    const group = element("div", { "data-docs-tabs": "" });
    const list = element("div", { "data-docs-tablist": "", "aria-label": "Examples" });
    const tabs = labels.map((_, i) => element("a", { id: name + "-tab-" + i, href: "#" + name + "-panel-" + i, "data-docs-tab": name + "-panel-" + i }));
    const panels = labels.map((_, i) => element("section", { id: name + "-panel-" + i, "data-docs-tabpanel": "" }));
    labels.forEach((label, i) => { tabs[i].textContent = label; panels[i].textContent = label + " content"; });
    list.append(...tabs);
    group.append(list, ...panels);
    const fixture = { group, list, tabs, panels };
    groups.push(fixture);
    return fixture;
  }
  for (const [name, labels] of options.tabs || []) tabGroup(name, labels);
  const navigation = new Element("details");
  navigation.open = options.navigationOpen !== false;
  const wide = new Element("media-query");
  wide.matches = options.wide === false;
  const documentEvents = new Element("document");
  const windowEvents = new Element("window");
  const window = {
    isSecureContext: options.secure !== false,
    location: { hash: options.hash || "" },
    addEventListener: (...args) => windowEvents.addEventListener(...args),
    emit: (...args) => windowEvents.emit(...args),
  };
  if (options.setup) options.setup({ groups, element, tabGroup, navigation });
  const document = {
    currentScript: { dataset: { search: "../_docs/search.json" } },
    baseURI: "https://example.test/team/product/guides/page.html",
    documentElement: new Element("html"),
    getElementById: id => ids.get(id),
    querySelector: selector => selector === ".mobile-navigation" ? navigation : null,
    querySelectorAll: selector => selector === "pre > code" ? [code] : selector === "[data-docs-tabs]" ? groups.map(fixture => fixture.group) : [],
    createElement: name => new Element(name),
    addEventListener: (...args) => documentEvents.addEventListener(...args),
    emit: (...args) => documentEvents.emit(...args),
  };
  const pending = new Map();
  const requests = [];
  const copied = [];
  let sequence = 0;
  const context = {
    document,
    URL,
    Event: class { constructor(type) { this.type = type; } },
    matchMedia: query => query === "(max-width: 760px)" ? wide : ({ matches: options.dark || false }),
    localStorage: {
      getItem() { if (options.storageError) throw new Error("Storage disabled"); return options.theme; },
      setItem(_, value) { if (options.storageError) throw new Error("Storage disabled"); options.theme = value; },
    },
    navigator: { clipboard: { async writeText(value) { if (options.copyError) throw new Error("Denied"); copied.push(value); } } },
    window,
    fetch(url) {
      requests.push(url.href);
      return options.fetch ? options.fetch(url) : Promise.resolve({ ok: true, json: async () => options.entries || [] });
    },
    setTimeout(callback) { const id = ++sequence; pending.set(id, callback); return id; },
    clearTimeout(id) { pending.delete(id); },
  };
  vm.runInNewContext(source, context, { timeout: 1000 });
  return {
    ids, document, window, requests, copied, code, pre, groups, navigation,
    focused: () => focused,
    scrolled: () => scrolled,
    pending: () => pending.size,
    async viewport(isWide) { wide.matches = !isWide; await wide.emit("change"); },
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

test("tabs progressively install relationships and keep independent selections", async () => {
  const app = reader({ tabs: [["code", ["Go", "cURL", "JavaScript"]], ["platform", ["Linux", "Windows"]]] });
  const [code, platform] = app.groups;
  for (const fixture of app.groups) {
    assert.equal(fixture.group.classList.contains("docs-tabs-ready"), true);
    assert.equal(fixture.list.getAttribute("role"), "tablist");
    fixture.tabs.forEach((tab, i) => {
      assert.equal(tab.getAttribute("role"), "tab");
      assert.equal(tab.getAttribute("aria-controls"), fixture.panels[i].id);
      assert.equal(tab.getAttribute("aria-selected"), String(i === 0));
      assert.equal(tab.tabIndex, i === 0 ? 0 : -1);
      assert.equal(fixture.panels[i].getAttribute("role"), "tabpanel");
      assert.equal(fixture.panels[i].getAttribute("aria-labelledby"), tab.id);
      assert.equal(fixture.panels[i].tabIndex, 0);
      assert.equal(fixture.panels[i].hidden, i !== 0);
    });
  }
  const click = await code.tabs[2].emit("click");
  assert.equal(click.defaultPrevented, true);
  assert.equal(app.focused(), code.tabs[2]);
  assert.equal(code.panels[2].hidden, false);
  assert.equal(code.panels[0].hidden, true);
  assert.equal(platform.panels[0].hidden, false);
  assert.equal(platform.tabs[0].getAttribute("aria-selected"), "true");
  assert.equal(app.requests.length, 0);
  assert.equal(app.pending(), 0, "tab initialization and clicks schedule no timers");
});

test("tabs support arrow wrapping and Home/End without intercepting other keys or modified clicks", async () => {
  const app = reader({ tabs: [["code", ["Go", "cURL", "JavaScript"]]] });
  const { tabs, panels } = app.groups[0];
  let event = await tabs[0].emit("keydown", { key: "ArrowLeft" });
  assert.equal(event.defaultPrevented, true);
  assert.equal(app.focused(), tabs[2]);
  assert.equal(panels[2].hidden, false);
  await tabs[2].emit("keydown", { key: "ArrowRight" });
  assert.equal(app.focused(), tabs[0]);
  await tabs[0].emit("keydown", { key: "End" });
  assert.equal(app.focused(), tabs[2]);
  await tabs[2].emit("keydown", { key: "Home" });
  assert.equal(app.focused(), tabs[0]);
  await tabs[0].emit("keydown", { key: "ArrowRight" });
  assert.equal(app.focused(), tabs[1]);
  assert.equal(tabs.filter(tab => tab.tabIndex === 0).length, 1);
  for (const extra of [{ key: "ArrowDown" }, { key: "PageDown" }, { key: "ArrowRight", ctrlKey: true }, { key: "End", metaKey: true }]) {
    event = await tabs[1].emit("keydown", extra);
    assert.equal(event.defaultPrevented, false);
    assert.equal(app.focused(), tabs[1]);
  }
  for (const extra of [{ ctrlKey: true }, { metaKey: true }, { shiftKey: true }, { altKey: true }, { button: 1 }]) {
    event = await tabs[0].emit("click", extra);
    assert.equal(event.defaultPrevented, false);
    assert.equal(panels[1].hidden, false);
  }
});

test("malformed tab relationships retain readable fallback without partial ARIA or hidden content", () => {
  const app = reader({ tabs: [["bad", ["One", "Two"]], ["duplicate", ["A", "B"]]], setup({ groups }) {
    groups[0].tabs[1].setAttribute("data-docs-tab", "missing-panel");
    groups[1].tabs[1].setAttribute("id", groups[1].tabs[0].id);
  } });
  for (const fixture of app.groups) {
    assert.equal(fixture.group.classList.contains("docs-tabs-ready"), false);
    assert.equal(fixture.list.getAttribute("role"), null);
    fixture.tabs.forEach(tab => assert.equal(tab.getAttribute("role"), null));
    fixture.panels.forEach(panel => {
      assert.equal(panel.hidden, false);
      assert.equal(panel.getAttribute("role"), null);
    });
  }
});

test("initial and repeated fragment links reveal nested tabs without moving keyboard focus", async () => {
  let target;
  let link;
  let other;
  const app = reader({ tabs: [["outer", ["Overview", "Details"]]], hash: "#nested-%CE%BB", setup({ groups, element, tabGroup }) {
    const inner = tabGroup("inner", ["First", "Second"]);
    groups[0].panels[1].append(inner.group);
    target = element("h3", { id: "nested-λ" });
    inner.panels[1].append(target);
    other = element("h3", { id: "overview-heading" });
    groups[0].panels[0].append(other);
    link = element("a", { href: "#nested-%CE%BB" });
  } });
  const [outer, inner] = app.groups;
  assert.equal(outer.panels[1].hidden, false);
  assert.equal(inner.panels[1].hidden, false);
  assert.equal(app.focused(), undefined);
  await outer.tabs[0].emit("click");
  assert.equal(outer.panels[1].hidden, true);
  await app.document.emit("click", { target: link });
  assert.equal(outer.panels[1].hidden, false, "same fragment click works even without a hashchange event");
  assert.equal(inner.panels[1].hidden, false);
  assert.equal(app.scrolled(), target);
  assert.equal(app.focused(), outer.tabs[0], "fragment activation preserves the reader's focus");
  app.window.location.hash = "#overview-heading";
  await app.window.emit("hashchange");
  assert.equal(outer.panels[0].hidden, false);
  assert.equal(app.scrolled(), other);
  app.window.location.hash = "#%";
  await app.window.emit("hashchange");
  assert.equal(outer.panels[0].hidden, false, "malformed URI fragments leave current content available");
});

test("responsive navigation opens on desktop and restores the mobile reader's toggle", async () => {
  const app = reader({ wide: false, navigationOpen: false });
  assert.equal(app.navigation.open, false);
  await app.viewport(true);
  assert.equal(app.navigation.open, true);
  await app.navigation.emit("toggle");
  await app.viewport(false);
  assert.equal(app.navigation.open, false);
  app.navigation.open = true;
  await app.navigation.emit("toggle");
  await app.viewport(true);
  await app.viewport(false);
  assert.equal(app.navigation.open, true);
  assert.equal(reader({ navigationOpen: false }).navigation.open, true, "restored collapsed state does not hide desktop navigation");
});
