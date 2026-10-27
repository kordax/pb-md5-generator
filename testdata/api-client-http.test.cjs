const test = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");
const client = require("../internal/site/api-client.js");

// These tests exercise Node Fetch over IPv4 loopback. They do not exercise a
// browser, CORS policy, credential storage, or any external API.
const testOptions = { timeout: 15000 };
function deferred() {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
}
function deadline(promise, label) {
  let timer;
  const guard = new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(label + " exceeded the test deadline")), 5000); });
  return Promise.race([promise, guard]).finally(() => clearTimeout(timer));
}
async function loopback(t, handler) {
  const failures = [];
  const server = http.createServer((request, response) => {
    Promise.resolve().then(() => handler(request, response)).catch(error => {
      failures.push(error);
      response.destroy();
    });
  });
  server.requestTimeout = 5000;
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => { server.removeListener("error", reject); resolve(); });
  });
  t.after(async () => {
    const closed = new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
    server.closeAllConnections();
    await deadline(closed, "Loopback cleanup");
    assert.deepEqual(failures, [], "loopback handler failed");
  });
  return "http://127.0.0.1:" + server.address().port;
}
async function requestBody(request) {
  const parts = [];
  let size = 0;
  for await (const part of request) {
    size += part.byteLength;
    if (size > 16384) throw new Error("Loopback request exceeded the fixture limit");
    parts.push(part);
  }
  return Buffer.concat(parts).toString("utf8");
}
function contract(origin, overrides = {}, schemes = []) {
  const server = { url: origin + "/v1", variables: [] };
  const operation = { id: "local", method: "GET", path: "/response", parameters: [], servers: [server], security: [], responses: [], ...overrides };
  return { api: { protocol: "http", servers: [server], operations: [operation], securitySchemes: schemes, security: [] }, operation };
}
function build(origin, overrides, input = {}, schemes) {
  const { api, operation } = contract(origin, overrides, schemes);
  return client.buildRequest(api, operation, input);
}

test("loopback receives exact serialized URL, headers and original JSON body bytes", testOptions, async t => {
  const seen = deferred();
  const origin = await loopback(t, async (request, response) => {
    seen.resolve({ method: request.method, url: request.url, headers: request.headers, body: await requestBody(request) });
    response.writeHead(201, { "Content-Type": "text/plain; charset=utf-8", "X-Request-ID": "local-wire" });
    response.end("created");
  });
  const json = '{\n "id":18446744073709551615,"decimal":1.234567890123456789\n}';
  const request = build(origin, {
    method: "POST", path: "/pets/{id}",
    parameters: [
      { name: "id", in: "path", required: true },
      { name: "tag", in: "query", style: "form", explode: true },
      { name: "filter", in: "query", style: "deepObject", explode: true },
      { name: "X-Coordinates", in: "header", style: "simple", explode: true },
    ],
    requestBody: { required: true, content: [{ name: "application/json" }] },
  }, {
    values: { "path:id": "Ж /?", "query:tag": ["blue green", "a&b"], "query:filter": client.parseParameterInput('{"id":9007199254740993,"active":true}', {}), "header:X-Coordinates": { R: "100", G: "200" } },
    body: json, mediaType: "application/json", accept: "text/plain",
  });
  const job = client.executeRequest(request, { timeoutMs: 5000 });
  t.after(job.cancel);
  const result = await deadline(job.promise, "Serialized request");
  const actual = await deadline(seen.promise, "Captured request");
  assert.equal(actual.method, "POST");
  const url = new URL(actual.url, origin);
  assert.equal(url.pathname, "/v1/pets/%D0%96%20%2F%3F");
  assert.deepEqual(url.searchParams.getAll("tag"), ["blue green", "a&b"]);
  assert.equal(url.searchParams.get("filter[id]"), "9007199254740993");
  assert.equal(url.searchParams.get("filter[active]"), "true");
  assert.equal(actual.headers["x-coordinates"], "R=100,G=200");
  assert.equal(actual.headers["content-type"], "application/json");
  assert.equal(actual.headers.accept, "text/plain");
  assert.equal(actual.headers.cookie, undefined);
  assert.equal(actual.body, json);
  assert.equal(result.status, 201);
  assert.equal(result.text, "created");
  assert.equal(result.truncated, false);
});

test("loopback response data preserves UTF-8 and markup as text and redacts sensitive headers", testOptions, async t => {
  const body = '<img src=x onerror=alert(1)> Ж☀ {"id":18446744073709551615}';
  const origin = await loopback(t, (_, response) => {
    response.writeHead(422, { "Content-Type": "text/plain; charset=utf-8", "X-API-Key": "synthetic-response-value", "X-Request-ID": "local-response" });
    response.end(body);
  });
  const result = await deadline(client.executeRequest(build(origin), { timeoutMs: 5000 }).promise, "Response data");
  assert.equal(result.status, 422);
  assert.equal(result.text, body);
  assert.equal(result.bytes, Buffer.byteLength(body));
  assert.equal(result.truncated, false);
  assert.ok(result.headers.some(([name, value]) => name === "x-request-id" && value === "local-response"));
  assert.ok(result.headers.some(([name, value]) => name === "x-api-key" && value === "[redacted]"));
  assert.ok(!JSON.stringify(result).includes("synthetic-response-value"));
  assert.deepEqual(Object.keys(result).sort(), ["bytes", "headers", "status", "statusText", "text", "truncated"]);
});

test("loopback streaming stops at the default byte limit and cancels the unfinished response", testOptions, async t => {
  const closed = deferred();
  const origin = await loopback(t, (_, response) => {
    response.on("close", () => closed.resolve({ ended: response.writableEnded }));
    response.writeHead(200, { "Content-Type": "text/plain" });
    // One finite write exceeds the ceiling; no producer loop or end marker can
    // hide whether the client canceled its reader after truncating the body.
    response.write(Buffer.alloc(client.LIMITS.response + 4096, 120));
  });
  const job = client.executeRequest(build(origin), { timeoutMs: 5000 });
  t.after(job.cancel);
  const result = await deadline(job.promise, "Limited response");
  assert.equal(result.bytes, client.LIMITS.response);
  assert.equal(result.text.length, client.LIMITS.response);
  assert.equal(result.text, "x".repeat(client.LIMITS.response));
  assert.equal(result.truncated, true);
  assert.deepEqual(await deadline(closed.promise, "Truncated response cancellation"), { ended: false });
});

test("loopback explicit cancel, external abort and scheduled timeout close pending streams", testOptions, async t => {
  for (const mode of ["cancel", "external", "timeout"]) {
    await t.test(mode, async t => {
      const headers = deferred(), closed = deferred();
      const origin = await loopback(t, (_, response) => {
        response.on("close", () => closed.resolve({ ended: response.writableEnded }));
        response.writeHead(200, { "Content-Type": "text/plain" });
        response.write("waiting for cancellation");
      });
      const external = new AbortController();
      let fireTimeout, scheduled = 0, cleared = 0;
      const token = {};
      const options = {
        timeoutMs: mode === "timeout" ? 30000 : 5000,
        signal: external.signal,
        async fetch(url, init) { const response = await fetch(url, init); headers.resolve(response); return response; },
      };
      if (mode === "timeout") {
        options.setTimeout = (callback, milliseconds) => { assert.equal(milliseconds, 30000); scheduled++; fireTimeout = callback; return token; };
        options.clearTimeout = value => { assert.equal(value, token); cleared++; };
      }
      const job = client.executeRequest(build(origin), options);
      t.after(job.cancel);
      const rejected = assert.rejects(job.promise, mode === "timeout" ? /Request timed out after 30000 ms/ : /Request canceled/);
      const response = await deadline(headers.promise, "Streaming headers");
      if (mode === "cancel") job.cancel();
      else if (mode === "external") external.abort();
      else fireTimeout(); // The actual Fetch is canceled by the client's timer callback; no short wall-clock race.
      await deadline(rejected, "Aborted request");
      assert.equal(job.signal.aborted, true);
      assert.equal(response.body.locked, false);
      assert.deepEqual(await deadline(closed.promise, "Aborted stream cancellation"), { ended: false });
      if (mode === "timeout") { assert.equal(scheduled, 1); assert.equal(cleared, 1); }
    });
  }
});

test("loopback redirects never replay a credential or body to a second origin", testOptions, async t => {
  let targetRequests = 0;
  const target = await loopback(t, (_, response) => { targetRequests++; response.end("must not be reached"); });
  const received = [];
  const source = await loopback(t, async (request, response) => {
    const status = Number(request.url.split("/").at(-1));
    received.push({ method: request.method, authorization: request.headers.authorization, body: await requestBody(request) });
    response.writeHead(status, { Location: target + "/replayed", "Content-Type": "text/plain" });
    response.end("redirect source");
  });
  const body = '{"synthetic":"loopback body"}';
  for (const status of [301, 302, 303, 307, 308]) {
    const request = build(source, { method: "POST", path: "/redirect/" + status, security: [{ bearer: [] }], requestBody: { required: true, content: [{ name: "application/json" }] } }, {
      body, mediaType: "application/json", credentials: { bearer: { value: "synthetic-loopback-token" } },
    }, [{ id: "bearer", type: "http", scheme: "bearer" }]);
    assert.ok(!request.preview.includes("synthetic-loopback-token"));
    const job = client.executeRequest(request, { timeoutMs: 5000 });
    t.after(job.cancel);
    await assert.rejects(deadline(job.promise, "Redirect rejection"), error => /Redirects are not followed/.test(error.message) && !error.message.includes("synthetic-loopback-token") && !error.message.includes(body));
  }
  assert.equal(received.length, 5);
  for (const request of received) {
    assert.equal(request.method, "POST");
    assert.equal(request.authorization, "Bearer synthetic-loopback-token");
    assert.equal(request.body, body);
  }
  assert.equal(targetRequests, 0, "redirect target must never receive a replay, including a rewritten GET");
});
