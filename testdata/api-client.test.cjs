const test = require("node:test");
const assert = require("node:assert/strict");
const { getEventListeners } = require("node:events");
const client = require("../internal/site/api-client.js");

function fixture(overrides = {}) {
  const server = { url: "https://api.example.test/v1", variables: [] };
  const operation = { id: "getPet", method: "GET", path: "/pets/{id}", parameters: [{ name: "id", in: "path", required: true }], servers: [server], security: [], responses: [] };
  const api = { protocol: "http", servers: [server], operations: [operation], securitySchemes: [], security: [] };
  return { api, operation: { ...operation, ...overrides } };
}
function request(overrides = {}, input = {}) {
  const { api, operation } = fixture(overrides);
  return client.buildRequest(api, operation, { values: { "path:id": "pet-1" }, ...input });
}
function responseReader(chunks, failure) {
  let reads = 0, canceled = 0, released = 0;
  const reader = {
    async read() { if (failure) throw failure; return reads < chunks.length ? { done: false, value: chunks[reads++] } : { done: true }; },
    cancel() { canceled++; return Promise.resolve(); },
    releaseLock() { released++; },
  };
  const response = { status: 200, statusText: "OK", headers: new Headers({ "content-type": "text/plain" }), body: { getReader: () => reader } };
  return { response, reader, canceled: () => canceled, released: () => released };
}

test("request builder appends operation paths and preserves raw JSON integers", () => {
  const json = '{\n  "id": 18446744073709551615, "name":"Alice"\n}';
  const result = request({ method: "POST", requestBody: { required: true, content: [{ name: "application/json" }] } }, { body: json, mediaType: "application/json" });
  assert.equal(result.url, "https://api.example.test/v1/pets/pet-1");
  assert.equal(result.body, json);
  assert.equal(result.headers["Content-Type"], "application/json");
  assert.equal(result.credentials, "omit");
  assert.equal(result.method, "POST");
  for (const method of ["QUERY", "PROPFIND", "X-REPORT"]) {
    const custom = request({ method, requestBody: { content: [{ name: "application/json" }] } }, { body: json, mediaType: "application/json" });
    assert.equal(custom.method, method); assert.equal(custom.body, json);
  }
  assert.ok(!result.preview.includes("Alice"), "preview does not duplicate possibly sensitive bodies");
  assert.throws(() => request({ requestBody: { required: true, content: [{ name: "application/json" }] } }), /requires a request body/);
  assert.throws(() => request({ requestBody: { content: [{ name: "application/json" }] } }, { body: "{}", mediaType: "application/json" }), /GET or HEAD/);
});

test("RFC6570 path, query and header styles keep their declared delimiters", () => {
  const examples = [
    [{ in: "path", style: "simple" }, ["blue", "black"], { path: "blue,black" }],
    [{ in: "path", style: "label", explode: true }, ["blue", "black"], { path: ".blue.black" }],
    [{ in: "path", style: "matrix", explode: true }, ["blue", "black"], { path: ";color=blue;color=black" }],
    [{ in: "path", style: "matrix" }, { R: 100, G: 200 }, { path: ";color=R,100,G,200" }],
    [{ in: "path", style: "label", explode: true }, { R: 100, G: 200 }, { path: ".R=100.G=200" }],
    [{ in: "query" }, ["blue", "black"], { query: [["color", "blue"], ["color", "black"]] }],
    [{ in: "query", explode: false }, { R: 100, G: 200 }, { query: [["color", "R,100,G,200"]] }],
    [{ in: "query", explode: true }, { R: 100, G: 200 }, { query: [["R", "100"], ["G", "200"]] }],
    [{ in: "query", style: "deepObject" }, { R: 100, G: 200 }, { query: [["color[R]", "100"], ["color[G]", "200"]] }],
    [{ in: "query", style: "spaceDelimited", explode: false }, ["a", "b"], { query: [["color", "a%20b"]] }],
    [{ in: "query", style: "pipeDelimited", explode: false }, { a: "x", b: "y" }, { query: [["color", "a%7Cx%7Cb%7Cy"]] }],
    [{ in: "header", style: "simple", explode: true }, { R: 100, G: 200 }, { header: "R=100,G=200" }],
    [{ in: "header" }, "Hello %2C world!", { header: "Hello %2C world!" }],
  ];
  for (const [parameter, value, expected] of examples) assert.deepEqual(client.serializeParameter({ name: "color", ...parameter }, value), expected);
  assert.deepEqual(client.serializeParameter({ name: "q", in: "query", allowReserved: true }, "x%2By/z?ok=yes"), { query: [["q", "x%2By/z?ok=yes"]] });
  assert.deepEqual(client.serializeParameter({ name: "q", in: "query" }, "a&b+c"), { query: [["q", "a%26b%2Bc"]] });
  assert.throws(() => client.serializeParameter({ name: "q", in: "query", allowReserved: true }, "x&admin=true"), /Percent-encode/);
  assert.throws(() => client.serializeParameter({ name: "q", in: "query", style: "spaceDelimited", explode: true }, ["a"]), /Unsupported/);
  assert.throws(() => client.serializeParameter({ name: "q", in: "query", style: "deepObject" }, { a: { b: 1 } }), /flat arrays or objects/);
  assert.throws(() => client.serializeParameter({ name: "q", in: "cookie" }, "value"), /browser-managed/);
  assert.throws(() => client.serializeParameter({ name: "q", in: "querystring" }, "value"), /Whole-querystring/);
});

test("content parameters preserve serialized JSON while typed inputs reject lossy numbers", () => {
  const parameter = { name: "filter", in: "query", content: [{ name: "application/json" }] };
  const text = '{"id":9007199254740993}';
  assert.equal(client.parseParameterInput(text, parameter), text);
  assert.deepEqual(client.serializeParameter(parameter, text), { query: [["filter", "%7B%22id%22%3A9007199254740993%7D"]] });
  assert.deepEqual(client.parseParameterInput("[9007199254740993,1.234567890123456789,-4e40]", {}), ["9007199254740993", "1.234567890123456789", "-4e40"]);
  assert.deepEqual(client.parseParameterInput('{"id":18446744073709551615}', {}), { id: "18446744073709551615" });
  assert.throws(() => client.serializeParameter({ name: "id", in: "query" }, 9007199254740993), /exact range/);
  assert.deepEqual(client.parseParameterInput('["9007199254740993"]', {}), ["9007199254740993"]);
  assert.equal(client.parseParameterInput('"[literal]"', {}), "[literal]");
  assert.throws(() => client.parseParameterInput("{not json}", {}), /must be JSON/);
  assert.throws(() => client.serializeParameter(parameter, "not JSON"), /valid JSON/);
});

test("server variables and relative servers require an explicit validated base", () => {
  const { api, operation } = fixture();
  operation.servers = [{ url: "https://{region}.example.test/{version}", variables: [{ name: "region", default: "eu", enum: ["eu", "us"] }, { name: "version", default: "v2" }] }];
  let result = client.buildRequest(api, operation, { values: { "path:id": "Ж" }, variables: { region: "us" } });
  assert.equal(result.url, "https://us.example.test/v2/pets/%D0%96");
  assert.throws(() => client.buildRequest(api, operation, { values: { "path:id": "x" }, variables: { region: "other" } }), /declared choices/);
  operation.servers = [{ url: "./v3" }];
  assert.throws(() => client.buildRequest(api, operation, { values: { "path:id": "x" } }), /absolute HTTP/);
  result = client.buildRequest(api, operation, { values: { "path:id": "x" }, baseURL: "https://target.example.test/contracts/openapi.json" });
  assert.equal(result.url, "https://target.example.test/contracts/v3/pets/x");
  for (const serverURL of ["file:///etc/passwd", "javascript:alert(1)", "https://user:pass@example.test", "https://example.test/#fragment", "//other.example.test"]) assert.throws(() => request({}, { serverURL }), /HTTP|credentials|absolute/);
  assert.throws(() => request({}, { serverIndex: -1 }), /valid server/);
  assert.throws(() => request({}, { baseURL: "https://user:pass@example.test/base" }), /credentials/);
});

test("security alternatives and combined schemes are explicit and previews redact secrets", () => {
  const { api, operation } = fixture();
  api.securitySchemes = [
    { id: "key", type: "apiKey", name: "api_key", in: "query" },
    { id: "bearer", type: "http", scheme: "bearer" },
    { id: "header", type: "apiKey", name: "X-API-Key", in: "header" },
    { id: "basic", type: "http", scheme: "basic" },
    { id: "oauth", type: "oauth2" },
    { id: "oidc", type: "openIdConnect" },
  ];
  operation.security = [{ key: [], bearer: [] }, {}, { basic: [] }, { oauth: ["read:pets"] }, { oidc: [] }];
  const input = { values: { "path:id": "pet-1" }, credentials: { key: { value: "unit-test-key" }, bearer: { value: "unit-test-token" } } };
  const result = client.buildRequest(api, operation, input);
  assert.equal(new URL(result.url).searchParams.get("api_key"), "unit-test-key");
  assert.equal(result.headers.Authorization, "Bearer unit-test-token");
  assert.ok(!result.preview.includes("unit-test-key") && !result.preview.includes("unit-test-token"));
  assert.throws(() => client.buildRequest(api, operation, { values: input.values }), /required/i);
  const anonymous = client.buildRequest(api, operation, { values: input.values, securityIndex: 1 });
  assert.equal(Object.keys(anonymous.headers).length, 0);
  const basic = client.buildRequest(api, operation, { values: input.values, securityIndex: 2, credentials: { basic: { username: "alice", password: "unit-test-password" } } });
  assert.equal(basic.headers.Authorization, "Basic " + Buffer.from("alice:unit-test-password").toString("base64"));
  assert.ok(!basic.preview.includes("unit-test-password"));
  for (const [index, scheme] of [[3, "oauth"], [4, "oidc"]]) assert.equal(client.buildRequest(api, operation, { values: input.values, securityIndex: index, credentials: { [scheme]: { value: "unit-test-token" } } }).headers.Authorization, "Bearer unit-test-token");
  operation.security = [{ header: [] }];
  operation.parameters.push({ in: "header", name: "x-api-key" });
  assert.throws(() => client.buildRequest(api, operation, { values: { ...input.values, "header:x-api-key": "collision" }, credentials: { header: { value: "unit-test-key" } } }), /Conflicting request header/);
  operation.security = [];
  api.security = [{ key: [] }];
  assert.equal(client.buildRequest(api, operation, { values: input.values }).url, "https://api.example.test/v1/pets/pet-1", "explicit anonymous overrides inherited security");
});

test("unsafe or ambiguous requests fail before sending", () => {
  assert.throws(() => request({}, { values: {} }), /Required parameter/);
  assert.throws(() => request({}, { values: { "path:id": ".." } }), /Dot path segments/);
  assert.throws(() => request({ path: "/pets/{unknown}" }), /placeholder/);
  for (const method of ["CONNECT", "TRACE", "TRACK"]) assert.throws(() => request({ method }), /cannot be sent/);
  assert.throws(() => request({ method: "GET\r\nInjected: value" }), /HTTP method cannot be sent/);
  assert.throws(() => request({ interaction: "webhook" }), /incoming interactions/);
  assert.throws(() => request({ interaction: "callback" }), /incoming interactions/);
  assert.throws(() => request({ parameters: [{ name: "q", in: "query", required: true }] }, { values: { "query:q": [] } }), /must not be empty/);
  assert.throws(() => request({ parameters: [{ name: "Cookie", in: "header" }], path: "/pets" }, { values: { "header:Cookie": "x=y" } }), /controlled by the browser/);
  assert.throws(() => request({ parameters: [{ name: "X-Name", in: "header" }], path: "/pets" }, { values: { "header:X-Name": "name\r\nInjected: yes" } }), /single-line/);
  assert.throws(() => request({ requestBody: { content: [{ name: "multipart/form-data" }] }, method: "POST" }, { body: "bytes", mediaType: "multipart/form-data" }), /external client/);
  assert.throws(() => request({ requestBody: { content: [{ name: "application/json" }] }, method: "POST" }, { body: "{}", mediaType: "text/plain" }), /declared request media/);
  const { api, operation } = fixture(); api.protocol = "grpc";
  assert.throws(() => client.buildRequest(api, operation), /HTTP operations only/);
  api.protocol = "http"; api.securitySchemes = [{ id: "cookie", type: "apiKey", in: "cookie", name: "session" }]; operation.security = [{ cookie: [] }];
  assert.throws(() => client.buildRequest(api, operation, { values: { "path:id": "x" } }), /Cookie API keys/);
});

test("response preview counts bytes, decodes split UTF-8 and cancels truncated/error streams", async () => {
  const bytes = new TextEncoder().encode("Ж☀");
  const full = responseReader([bytes.slice(0, 1), bytes.slice(1, 3), bytes.slice(3)]);
  const decoded = await client.readResponse(full.response);
  assert.equal(decoded.text, "Ж☀"); assert.equal(decoded.bytes, 5); assert.equal(decoded.truncated, false);
  assert.equal(full.released(), 1); assert.equal(full.canceled(), 0);
  const limited = responseReader([new Uint8Array(100).fill(120)]);
  const preview = await client.readResponse(limited.response, { maxBytes: 5 });
  assert.equal(preview.text, "xxxxx"); assert.equal(preview.bytes, 5); assert.equal(preview.truncated, true);
  assert.equal(limited.canceled(), 1); assert.equal(limited.released(), 1);
  const zeroChunks = responseReader(Array.from({ length: 20 }, () => new Uint8Array()));
  assert.equal((await client.readResponse(zeroChunks.response, { maxChunks: 4 })).truncated, true);
  assert.equal(zeroChunks.canceled(), 1);
  const failed = responseReader([], new Error("stream failed"));
  await assert.rejects(client.readResponse(failed.response), /stream failed/);
  assert.equal(failed.canceled(), 1); assert.equal(failed.released(), 1);
  const headers = responseReader([]); headers.response.headers = new Headers({ Authorization: "unit-test-token" });
  assert.deepEqual((await client.readResponse(headers.response)).headers, [["authorization", "[redacted]"]]);
});

test("request execution defaults are credential-safe and completion clears timers/listeners", async () => {
  const timers = new Map(); let sequence = 0; let seen;
  const external = new AbortController();
  const job = client.executeRequest(request(), {
    signal: external.signal,
    setTimeout(callback) { const id = ++sequence; timers.set(id, callback); return id; }, clearTimeout(id) { timers.delete(id); },
    async fetch(url, options) { seen = { url, options }; return new Response("response", { status: 201 }); },
  });
  const result = await job.promise;
  assert.equal(result.status, 201); assert.equal(result.text, "response");
  assert.equal(seen.options.credentials, "omit"); assert.equal(seen.options.redirect, "error"); assert.equal(seen.options.referrerPolicy, "no-referrer");
  assert.equal(timers.size, 0); assert.equal(getEventListeners(external.signal, "abort").length, 0);
});

test("cancel and timeout abort pending requests without leaking timers or sensitive errors", async () => {
  for (const reason of ["cancel", "timeout"]) {
    let fire, removed = 0, aborted = false;
    const job = client.executeRequest(request(), {
      setTimeout(callback) { fire = callback; return 1; }, clearTimeout() { removed++; },
      fetch(_, options) { return new Promise((resolve, reject) => options.signal.addEventListener("abort", () => { aborted = true; reject(new Error("unit-test-secret")); }, { once: true })); },
    });
    if (reason === "cancel") job.cancel(); else fire();
    await assert.rejects(job.promise, reason === "cancel" ? /Request canceled/ : /timed out/);
    assert.equal(aborted, true); assert.equal(removed, 1);
  }
  const failed = client.executeRequest(request(), { fetch: async () => { throw new Error("unit-test-secret"); } });
  await assert.rejects(failed.promise, error => !error.message.includes("unit-test-secret") && /CORS/.test(error.message));
});

test("control and wire resource limits fail before target execution", () => {
  assert.throws(() => request({ parameters: Array.from({ length: client.LIMITS.parameters + 1 }, (_, index) => ({ in: "query", name: "p" + index })) }), /browser client's limit/);
  assert.throws(() => request({ servers: Array.from({ length: 33 }, () => ({ url: "https://api.example.test" })) }), /Server choices/);
  assert.throws(() => request({ security: Array.from({ length: 33 }, () => ({})) }), /Authentication alternatives/);
  assert.throws(() => request({ requestBody: { content: Array.from({ length: 65 }, () => ({ name: "application/json" })) } }), /Request media/);
  assert.throws(() => request({}, { values: { "path:id": "x".repeat(client.LIMITS.url) } }), /URL exceeds/);
  assert.throws(() => client.executeRequest({ url: "data:text/plain,example", method: "GET" }), /HTTP/);
  assert.throws(() => client.executeRequest({ url: "https://example.test/", method: "TRACE" }), /cannot be sent/);
});

test("default response ceiling and abort during streaming release the reader", async () => {
  const oversized = responseReader([new Uint8Array(client.LIMITS.response + 1).fill(120)]);
  const preview = await client.readResponse(oversized.response);
  assert.equal(preview.bytes, client.LIMITS.response); assert.equal(preview.truncated, true);
  assert.equal(oversized.canceled(), 1); assert.equal(oversized.released(), 1);
  let canceled = 0;
  const stream = new ReadableStream({ cancel() { canceled++; } });
  const job = client.executeRequest(request(), { fetch: async () => new Response(stream) });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(stream.locked, true);
  job.cancel();
  await assert.rejects(job.promise, /Request canceled/);
  assert.ok(canceled >= 1); assert.equal(stream.locked, false);
});

test("already aborted execution never calls Fetch", async () => {
  const signal = new AbortController(); signal.abort();
  let calls = 0;
  const job = client.executeRequest(request(), { signal: signal.signal, fetch: async () => { calls++; return new Response(); } });
  await assert.rejects(job.promise, /Request canceled/);
  assert.equal(calls, 0); assert.equal(getEventListeners(signal.signal, "abort").length, 0);
});

function dom(api, fetcher, contractFetcher, settings = undefined) {
  class Element {
    constructor(tag) { this.tagName = tag; this.children = []; this.dataset = {}; this.attributes = {}; this.events = new Map(); this.value = ""; this.hidden = false; this.disabled = false; this._text = ""; this.className = ""; }
    set textContent(text) { this._text = String(text); this.replaceChildren(); }
    get textContent() { return this._text + this.children.map(child => child.textContent).join(""); }
    set innerHTML(_) { throw new Error("Unsafe HTML rendering"); }
    setAttribute(name, value) { this.attributes[name] = String(value); }
    append(...nodes) { for (const node of nodes) { this.children.push(node); node.parentElement = this; } }
    replaceChildren(...nodes) { for (const child of this.children) child.parentElement = null; this.children = []; this.append(...nodes); }
    addEventListener(name, callback, options = {}) { const entries = this.events.get(name) || []; entries.push({ callback, once: options.once }); this.events.set(name, entries); }
    removeEventListener(name, callback) { this.events.set(name, (this.events.get(name) || []).filter(entry => entry.callback !== callback)); }
    matches(selector) { return selector.startsWith(".") ? this.className.split(" ").includes(selector.slice(1)) : selector === "[data-api-client]" ? this.isHost : selector === "[data-api-operation]" ? !!this.dataset.apiOperation : this.tagName === selector; }
    querySelectorAll(selector) { return this.children.flatMap(child => [...(child.matches(selector) ? [child] : []), ...child.querySelectorAll(selector)]); }
    querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
    closest(selector) { for (let item = this; item; item = item.parentElement) if (item.matches(selector)) return item; return null; }
    async emit(type, extra = {}) {
      const event = { type, target: this, preventDefault() {}, ...extra };
      for (let node = this; node; node = node.parentElement) {
        for (const entry of [...(node.events.get(type) || [])]) { if (entry.once) node.removeEventListener(type, entry.callback); await entry.callback(event); }
      }
    }
  }
  const body = new Element("body");
  const hosts = [], buttons = [];
  for (const operation of api.operations) {
    const section = new Element("section"); section.className = "api-operation";
    const button = new Element("button"); button.dataset.apiOperation = operation.id; button.dataset.apiUrl = "../_docs/pages/api.json";
    const host = new Element("div"); host.isHost = true; host.hidden = true;
    section.append(button, host); body.append(section); hosts.push(host); buttons.push(button);
  }
  const document = { baseURI: "https://docs.example.test/team/product/reference/api.html", createElement: tag => new Element(tag), querySelectorAll: selector => body.querySelectorAll(selector) };
  const calls = [];
  const dispose = client.attach(document, { settings, fetch: async (url, options) => {
    calls.push({ url, options });
    if (url.includes("/_docs/pages/")) return contractFetcher ? contractFetcher(url, options) : new Response(JSON.stringify({ version: 1, api }), { headers: { "content-type": "application/json" } });
    return fetcher ? fetcher(url, options) : new Response("{\"ok\":true}", { headers: { "content-type": "application/json" } });
  } });
  const field = (host, text) => host.querySelectorAll("label").find(label => label.children[0].textContent.startsWith(text)).children[1];
  return { body, document, buttons, hosts, calls, field, dispose };
}

test("enhancement fetches only the contract on Try and waits for a separate Send", async () => {
  const { api, operation } = fixture(); api.operations = [operation];
  const app = dom(api);
  try {
    assert.equal(app.calls.length, 0);
    await app.buttons[0].emit("click");
    assert.equal(app.calls.length, 1);
    assert.equal(app.calls[0].url, "https://docs.example.test/team/product/_docs/pages/api.json");
    const id = app.field(app.hosts[0], "path · id"); id.value = "pet-2";
    await id.emit("input"); assert.equal(app.calls.length, 1);
    await app.hosts[0].querySelector("form").emit("submit");
    assert.equal(app.calls.length, 2);
    assert.equal(app.calls[1].url, "https://api.example.test/v1/pets/pet-2");
    assert.equal(app.calls[1].options.credentials, "omit");
    assert.match(app.hosts[0].querySelector(".api-client-response").textContent, /"ok":true/);
  } finally { app.dispose(); }
});

test("only one panel stays active, contract is cached and closing wipes credential inputs", async () => {
  const { api, operation } = fixture();
  api.securitySchemes = [{ id: "key", type: "apiKey", in: "header", name: "X-API-Key" }];
  operation.security = [{ key: [] }]; api.operations = [operation, { ...operation, id: "getOther", path: "/other/{id}" }];
  const app = dom(api);
  try {
    await app.buttons[0].emit("click");
    const secret = app.hosts[0].querySelectorAll("input").find(input => input.type === "password");
    secret.value = "unit-test-key"; await secret.emit("input");
    assert.ok(!app.hosts[0].querySelector(".api-client-preview").textContent.includes("unit-test-key"));
    await app.buttons[1].emit("click");
    assert.equal(app.calls.length, 1, "opening another operation reuses the bounded contract cache");
    assert.equal(secret.value, ""); assert.equal(app.hosts[0].hidden, true);
    assert.equal(app.hosts[1].hidden, false);
    await app.hosts[1].querySelectorAll("button").find(button => button.textContent === "Close and clear").emit("click");
    assert.equal(app.hosts[1].hidden, true);
  } finally { app.dispose(); }
  assert.equal(app.buttons[0].events.get("click").length, 0);
});

test("failed contracts can be retried and loading can be canceled without target traffic", async () => {
  const { api, operation } = fixture(); api.operations = [operation];
  let attempts = 0;
  const app = dom(api, undefined, async () => ++attempts === 1 ? new Response("not JSON") : new Response(JSON.stringify({ api })));
  try {
    await app.buttons[0].emit("click");
    assert.match(app.hosts[0].textContent, /invalid JSON/);
    await app.buttons[0].emit("click");
    assert.ok(app.hosts[0].querySelector("form")); assert.equal(attempts, 2);
    assert.ok(app.calls.every(call => call.url.includes("/_docs/pages/")));
  } finally { app.dispose(); }
  let finish;
  const loading = dom(api, undefined, () => new Promise(resolve => { finish = resolve; }));
  try {
    const opening = loading.buttons[0].emit("click");
    await new Promise(resolve => setImmediate(resolve));
    await loading.hosts[0].querySelector("button").emit("click");
    assert.equal(loading.calls[0].options.signal.aborted, true);
    finish(new Response(JSON.stringify({ api })));
    await opening;
    assert.equal(loading.hosts[0].hidden, true); assert.equal(loading.hosts[0].children.length, 0);
  } finally { loading.dispose(); }
});

test("a stale response cannot replace a newer request result", async () => {
  const { api, operation } = fixture(); api.operations = [operation];
  let first, count = 0;
  const app = dom(api, async () => ++count === 1 ? new Promise(resolve => { first = resolve; }) : new Response("new response"));
  try {
    await app.buttons[0].emit("click"); app.field(app.hosts[0], "path · id").value = "pet-1";
    const form = app.hosts[0].querySelector("form");
    const old = form.emit("submit");
    await new Promise(resolve => setImmediate(resolve));
    await form.emit("submit");
    first(new Response("old response")); await old;
    const text = app.hosts[0].querySelector(".api-client-response").textContent;
    assert.match(text, /new response/); assert.ok(!text.includes("old response"));
  } finally { app.dispose(); }
});

test("auto-filled examples and schema defaults preserve large scalar, array, object and body numbers", async () => {
  const { api, operation } = fixture();
  operation.method = "POST";
  operation.parameters = [
    { name: "id", in: "path", required: true, example: 9007199254740993, exampleJSON: "9007199254740993" },
    { name: "ids", in: "query", schema: "contract.json#/schemas/Ids" },
    { name: "filter", in: "query", style: "deepObject", examples: [{ name: "big", value: { count: 18446744073709551615 }, valueJSON: '{"count":18446744073709551615}' }] },
    { name: "limit", in: "query", schema: "contract.json#/schemas/Alias" },
  ];
  api.schemas = [
    { id: "contract.json#/schemas/Ids", definition: { type: "array", default: [18446744073709551615] }, defaultJSON: "[18446744073709551615,9007199254740993]" },
    { id: "contract.json#/schemas/Alias", source: { file: "contract.json" }, definition: { $ref: "#/schemas/Integer" } },
    { id: "contract.json#/schemas/Integer", definition: { type: "integer", default: 18446744073709551615 }, defaultJSON: "18446744073709551615" },
    { id: "contract.json#/schemas/Body", definition: { type: "object" }, defaultJSON: '{"id":18446744073709551615,"fraction":0.1234567890123456789}' },
  ];
  operation.requestBody = { required: true, content: [{ name: "application/json", schema: "contract.json#/schemas/Body" }] };
  api.operations = [operation];
  const app = dom(api);
  try {
    await app.buttons[0].emit("click");
    assert.equal(app.field(app.hosts[0], "path · id").value, "9007199254740993");
    assert.equal(app.field(app.hosts[0], "query · ids").value, "[18446744073709551615,9007199254740993]");
    await app.hosts[0].querySelector("form").emit("submit");
    const sent = app.calls[1]; const url = new URL(sent.url);
    assert.equal(url.pathname, "/v1/pets/9007199254740993");
    assert.deepEqual(url.searchParams.getAll("ids"), ["18446744073709551615", "9007199254740993"]);
    assert.equal(url.searchParams.get("filter[count]"), "18446744073709551615");
    assert.equal(url.searchParams.get("limit"), "18446744073709551615");
    assert.equal(sent.options.body, '{"id":18446744073709551615,"fraction":0.1234567890123456789}');
  } finally { app.dispose(); }
});

test("serialized examples avoid double encoding and JSON text media examples keep wire bytes", async () => {
  const { api, operation } = fixture(); operation.method = "POST";
  operation.parameters[0].exampleJSON = '"pet-1"';
  operation.parameters.push({ name: "ids", in: "query", examples: [{ name: "wire", serializedValue: "ids=18446744073709551615,9007199254740993" }] });
  const wire = '{\n"id":18446744073709551615\n}';
  operation.requestBody = { required: true, content: [{ name: "application/json", examples: [{ name: "wire", valueJSON: '{"id":18446744073709551615}', serializedValue: wire }] }, { name: "text/plain", exampleJSON: '"18446744073709551615"' }] };
  api.operations = [operation]; const app = dom(api);
  try {
    await app.buttons[0].emit("click");
    assert.equal(app.field(app.hosts[0], "Already serialized wire value for ids").checked, true);
    await app.hosts[0].querySelector("form").emit("submit");
    assert.ok(app.calls[1].url.endsWith("?ids=18446744073709551615,9007199254740993"));
    assert.equal(app.calls[1].options.body, wire);
    const media = app.field(app.hosts[0], "Request media type"); media.value = "text/plain"; await media.emit("change");
    await app.hosts[0].querySelector("form").emit("submit");
    assert.equal(app.calls[2].options.body, "18446744073709551615");
    assert.equal(app.calls[2].options.headers["Content-Type"], "text/plain");
  } finally { app.dispose(); }
});

test("credential examples stay blank and recursive schema defaults do not expand", async () => {
  const { api, operation } = fixture(); operation.parameters[0].exampleJSON = '"pet-1"';
  api.securitySchemes = [{ id: "key", type: "apiKey", in: "query", name: "k" }];
  operation.parameters.push({ name: "k", in: "query", exampleJSON: '"unit-test-key"' }, { name: "loop", in: "query", schema: "contract.json#/Loop" });
  api.schemas = [{ id: "contract.json#/Loop", source: { file: "contract.json" }, definition: { $ref: "#/Loop" } }];
  api.operations = [operation]; const app = dom(api);
  try {
    await app.buttons[0].emit("click");
    const credential = app.field(app.hosts[0], "query · k");
    assert.equal(credential.value, ""); assert.equal(credential.type, "password");
    assert.equal(app.field(app.hosts[0], "query · loop").value, "");
  } finally { app.dispose(); }
  for (const value of ["[01]", "[1.]", "[1e+]", "[-]", '["unterminated]']) assert.throws(() => client.parseParameterInput(value, {}), /must be JSON/);
  assert.deepEqual(client.parseParameterInput('["escaped\\\\path","quote\\\"1",9007199254740993]', {}), ["escaped\\path", 'quote"1', "9007199254740993"]);
});

test("response status text cannot echo credentials into the status region", async () => {
  const { api, operation } = fixture(); api.operations = [operation];
  const app = dom(api, async () => new Response("body", { status: 200, statusText: "unit-test-token" }));
  try {
    await app.buttons[0].emit("click"); app.field(app.hosts[0], "path · id").value = "pet-1";
    await app.hosts[0].querySelector("form").emit("submit");
    assert.equal(app.hosts[0].querySelector(".api-client-status").textContent, "HTTP 200");
  } finally { app.dispose(); }
});

test("reader settings hide cURL and enforce the configured response cap on an explicit request", async () => {
  const { api, operation } = fixture(); api.operations = [operation];
  const app = dom(api, async () => new Response("x".repeat(4096)), undefined, { showCurl: false, requestTimeoutMs: 1000, maxResponseBytes: 1024 });
  try {
    await app.buttons[0].emit("click");
    const host = app.hosts[0];
    assert.equal(host.querySelector(".api-client-curl"), null);
    assert.equal(host.querySelectorAll("button").some(button => button.textContent.includes("Copy cURL")), false);
    app.field(host, "path · id").value = "pet-1";
    app.field(host, "Additional headers").value = "X-Trace: trace-17";
    await host.querySelector("form").emit("submit");
    assert.equal(app.calls.length, 2);
    assert.equal(app.calls[1].options.headers["X-Trace"], "trace-17");
    assert.match(host.querySelector(".api-client-status").textContent, /truncated/);
    assert.ok(host.querySelector(".api-client-response").textContent.endsWith("x".repeat(1024)));
    assert.ok(!host.querySelector(".api-client-response").textContent.endsWith("x".repeat(1025)));
  } finally { app.dispose(); }
});

test("closing an in-flight panel aborts its request and stale output cannot reappear", async () => {
  const { api, operation } = fixture(); api.operations = [operation]; let aborted = false;
  const app = dom(api, (_, options) => new Promise((resolve, reject) => options.signal.addEventListener("abort", () => { aborted = true; reject(new Error("aborted")); }, { once: true })));
  try {
    await app.buttons[0].emit("click");
    app.field(app.hosts[0], "path · id").value = "pet-1";
    const submission = app.hosts[0].querySelector("form").emit("submit");
    await new Promise(resolve => setImmediate(resolve));
    await app.hosts[0].querySelectorAll("button").find(button => button.textContent === "Close and clear").emit("click");
    await submission;
    assert.equal(aborted, true); assert.equal(app.hosts[0].hidden, true); assert.equal(app.hosts[0].children.length, 0);
  } finally { app.dispose(); }
});
